package dmg

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ironpark/zapp/pkg/mactools/dsstore"
	"github.com/ironpark/zapp/pkg/mactools/hdiutil"
)

// Config represents the configuration for the DMG file.
type Config struct {
	FileName         string `json:"fileName"`
	Title            string `json:"title"`
	Icon             string `json:"icon"`
	LabelSize        int    `json:"labelSize"`
	ContentsIconSize int    `json:"iconSize"`
	WindowWidth      int    `json:"windowWidth"`
	WindowHeight     int    `json:"windowHeight"`
	Background       string `json:"background"`
	Contents         []Item `json:"contents"`
	LogWriter        io.Writer
	Format           hdiutil.Format `json:"format"`
	CompressionLevel string         `json:"compressionLevel"`
	UseHardLinks     bool           `json:"useHardLinks"`
	OptimizeAppSize  bool           `json:"optimizeAppSize"`
}

type ItemType string

const (
	Dir  ItemType = "dir"
	File ItemType = "file"
	Link ItemType = "link"
)

// Item represents an item in the DMG file.
type Item struct {
	X    int      `json:"x"`
	Y    int      `json:"y"`
	Type ItemType `json:"type"`
	Path string   `json:"path"`
}

// CreateDMG creates a DMG file with the specified configuration.
func CreateDMG(config Config, sourceDir string) error {
	// Если используются hard links, используем безопасный метод
	if config.UseHardLinks {
		return createDMGWithSafeHardLinks(config, sourceDir)
	}

	// Create the source directory if it doesn't exist
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		return fmt.Errorf("failed to create source directory: %w", err)
	}
	// Setup the source directory with the necessary files
	if err := setupSourceDirectory(config, sourceDir); err != nil {
		return fmt.Errorf("failed to setup source directory: %w", err)
	}
	if config.LogWriter == nil {
		config.LogWriter = os.Stdout
	}
	store := dsstore.NewDSStore()
	store.SetIconSize(float64(config.ContentsIconSize))
	store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
	store.SetLabelSize(float64(config.LabelSize))
	store.SetLabelPlaceToBottom(true)
	store.SetBgToDefault()
	for _, content := range config.Contents {
		store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
	}
	err := store.Write(filepath.Join(sourceDir, ".DS_Store"))
	if err != nil {
		return fmt.Errorf("failed to write .DS_Store: %w", err)
	}

	// Set Default Filename
	if config.FileName == "" {
		config.FileName = config.Title + ".dmg"
	}

	if !strings.HasSuffix(config.FileName, ".dmg") {
		config.FileName += ".dmg"
	}
	
	// Set default format if not specified
	if config.Format == "" {
		config.Format = hdiutil.UDZO // Default to compressed format
	}

	ctx := context.Background()
	
	// For compressed formats, create directly without intermediate conversion
	if config.Format == hdiutil.UDZO || config.Format == hdiutil.UDBZ {
		// Create compressed DMG directly
		if err := createCompressedDMG(ctx, config, sourceDir); err != nil {
			return fmt.Errorf("failed to create compressed dmg: %w", err)
		}
	} else {
		// Create the DMG file using hdiutil
		if err := hdiutil.Create(ctx, config.Title, sourceDir, config.Format, config.FileName); err != nil {
			return fmt.Errorf("failed to create dmg: %w", err)
		}

		// Convert to read-only if needed
		if config.Format == hdiutil.UDRW {
			tempFileName := filepath.Join(filepath.Dir(config.FileName), fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), filepath.Base(config.FileName)))
			if err := os.Rename(config.FileName, tempFileName); err != nil {
				return fmt.Errorf("failed to rename DMG file: %w", err)
			}
			defer os.Remove(tempFileName) // Ensure cleanup of temp file
			if err := hdiutil.Convert(ctx, tempFileName, hdiutil.UDRO, config.FileName); err != nil {
				return fmt.Errorf("failed to convert DMG: %w", err)
			}
		}
	}

	// Set custom icon for the DMG if specified
	if config.Icon != "" || config.Background != "" {
		err = tmpMount(config.FileName, func(dmgFilePath string, mountPoint string) error {
			if config.Icon != "" {
				if err := setDMGIcon(mountPoint, config.Icon); err != nil {
					return fmt.Errorf("failed to set DMG icon: %w", err)
				}
			}
			if config.Background != "" {
				store.SetBackgroundImage(filepath.Join(mountPoint, ".background", "background.png"))
				if err := store.Write(filepath.Join(mountPoint, ".DS_Store")); err != nil {
					return fmt.Errorf("failed to write .DS_Store: %w", err)
				}
			}
			return nil
		})
	}

	if config.Icon != "" {
		setFileIcon(config.FileName, config.Icon)
	}
	return nil
}

// CreateDMGDirect creates a DMG file directly from source files without intermediate copying
func CreateDMGDirect(config Config) error {
	if config.LogWriter == nil {
		config.LogWriter = os.Stdout
	}

	// Set Default Filename
	if config.FileName == "" {
		config.FileName = config.Title + ".dmg"
	}

	if !strings.HasSuffix(config.FileName, ".dmg") {
		config.FileName += ".dmg"
	}
	
	// Set default format if not specified
	if config.Format == "" {
		config.Format = hdiutil.UDZO // Default to compressed format
	}

	ctx := context.Background()
	
	// Create temporary working directory
	tempDir, err := os.MkdirTemp("", "*-zapp-dmg-direct")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Create symbolic links to original files instead of copying
	if err := createSymbolicLinks(config, tempDir); err != nil {
		return fmt.Errorf("failed to create symbolic links: %w", err)
	}

	// Create DS_Store file
	store := dsstore.NewDSStore()
	store.SetIconSize(float64(config.ContentsIconSize))
	store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
	store.SetLabelSize(float64(config.LabelSize))
	store.SetLabelPlaceToBottom(true)
	store.SetBgToDefault()
	for _, content := range config.Contents {
		store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
	}
	
	if err := store.Write(filepath.Join(tempDir, ".DS_Store")); err != nil {
		return fmt.Errorf("failed to write .DS_Store: %w", err)
	}

	// For compressed formats, create directly
	if config.Format == hdiutil.UDZO || config.Format == hdiutil.UDBZ {
		// Create compressed DMG directly
		if err := createCompressedDMGDirect(ctx, config, tempDir); err != nil {
			return fmt.Errorf("failed to create compressed dmg: %w", err)
		}
	} else {
		// Create the DMG file using hdiutil
		if err := hdiutil.Create(ctx, config.Title, tempDir, config.Format, config.FileName); err != nil {
			return fmt.Errorf("failed to create dmg: %w", err)
		}

		// Convert to read-only if needed
		if config.Format == hdiutil.UDRW {
			tempFileName := filepath.Join(filepath.Dir(config.FileName), fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), filepath.Base(config.FileName)))
			if err := os.Rename(config.FileName, tempFileName); err != nil {
				return fmt.Errorf("failed to rename DMG file: %w", err)
			}
			defer os.Remove(tempFileName) // Ensure cleanup of temp file
			if err := hdiutil.Convert(ctx, tempFileName, hdiutil.UDRO, config.FileName); err != nil {
				return fmt.Errorf("failed to convert DMG: %w", err)
			}
		}
	}

	// Set custom icon for the DMG if specified
	if config.Icon != "" {
		setFileIcon(config.FileName, config.Icon)
	}
	
	return nil
}

// CreateDMGLikeBash создает DMG по методу bash-скрипта с точным контролем размера и кастомизацией
func CreateDMGLikeBash(config Config) error {
	if config.LogWriter == nil {
		config.LogWriter = os.Stdout
	}

	// Set Default Filename
	if config.FileName == "" {
		config.FileName = config.Title + ".dmg"
	}

	if !strings.HasSuffix(config.FileName, ".dmg") {
		config.FileName += ".dmg"
	}

	// Create temporary DMG name
	tempDMG := filepath.Join(filepath.Dir(config.FileName), "temp_"+filepath.Base(config.FileName))
	defer os.Remove(tempDMG) // Clean up temp file

	ctx := context.Background()

	// Step 1: Calculate optimal DMG size
	var totalSize int64
	for _, item := range config.Contents {
		if item.Type == Dir {
			filepath.Walk(item.Path, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() {
					totalSize += info.Size()
				}
				return nil
			})
		}
	}

	// Convert to MB and add 50% overhead for filesystem
	sizeMB := int(totalSize/(1024*1024)) + int(float64(totalSize/(1024*1024))*0.5)
	// Minimum size 200MB, maximum 2000MB
	if sizeMB < 200 {
		sizeMB = 200
	}
	if sizeMB > 2000 {
		sizeMB = 2000
	}

	fmt.Fprintf(config.LogWriter, "Creating DMG with size: %dMB\n", sizeMB)

	// Step 2: Create temporary read-write DMG directly from app
	// Find the main app directory
	var mainAppPath string
	for _, item := range config.Contents {
		if item.Type == Dir && strings.HasSuffix(item.Path, ".app") {
			mainAppPath = item.Path
			break
		}
	}

	if mainAppPath == "" {
		return fmt.Errorf("no .app directory found in contents")
	}

	fmt.Fprintf(config.LogWriter, "Creating temporary DMG from %s\n", mainAppPath)
	
	// Create DMG with exact size
	if err := hdiutil.CreateWithSize(ctx, config.Title, mainAppPath, hdiutil.UDRW, tempDMG, sizeMB); err != nil {
		return fmt.Errorf("failed to create temp dmg: %w", err)
	}

	// Step 3: Mount and customize
	fmt.Fprintf(config.LogWriter, "Mounting and customizing DMG...\n")
	
	err := tmpMount(tempDMG, func(dmgFilePath string, mountPoint string) error {
		// Add Applications link
		if err := os.Symlink("/Applications", filepath.Join(mountPoint, "Applications")); err != nil {
			return fmt.Errorf("failed to create Applications link: %w", err)
		}

		// Set background if specified
		if config.Background != "" {
			backgroundDir := filepath.Join(mountPoint, ".background")
			if err := os.MkdirAll(backgroundDir, 0755); err != nil {
				return fmt.Errorf("failed to create .background directory: %w", err)
			}
			if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
				return fmt.Errorf("failed to copy background: %w", err)
			}
			
			// Make background folder invisible
			cmd := exec.Command("SetFile", "-a", "V", backgroundDir)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to hide background folder: %s, output: %s", err, string(output))
			}
		}

		// Set volume icon if specified
		if config.Icon != "" {
			if err := setDMGIcon(mountPoint, config.Icon); err != nil {
				return fmt.Errorf("failed to set DMG icon: %w", err)
			}
		}

		// Create DS_Store with custom layout
		store := dsstore.NewDSStore()
		store.SetIconSize(float64(config.ContentsIconSize))
		store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
		store.SetLabelSize(float64(config.LabelSize))
		store.SetLabelPlaceToBottom(true)
		store.SetBgToDefault()
		
		if config.Background != "" {
			store.SetBackgroundImage(filepath.Join(mountPoint, ".background", "background.png"))
		}
		
		for _, content := range config.Contents {
			store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
		}
		
		if err := store.Write(filepath.Join(mountPoint, ".DS_Store")); err != nil {
			return fmt.Errorf("failed to write .DS_Store: %w", err)
		}

		return nil
	})
	
	if err != nil {
		return err
	}

	// Step 4: Convert to final compressed format
	fmt.Fprintf(config.LogWriter, "Converting to compressed format...\n")
	
	var convertArgs []string
	if config.CompressionLevel != "" {
		convertArgs = append(convertArgs, "-imagekey", fmt.Sprintf("zlib-level=%s", config.CompressionLevel))
	} else {
		convertArgs = append(convertArgs, "-imagekey", "zlib-level=9")
	}
	
	cmd := exec.CommandContext(ctx, "hdiutil", append([]string{"convert", tempDMG, "-format", "UDZO", "-o", config.FileName}, convertArgs...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil convert failed: %w, output: %s", err, string(output))
	}

	// Step 5: Set file icon if specified
	if config.Icon != "" {
		setFileIcon(config.FileName, config.Icon)
	}

	fmt.Fprintf(config.LogWriter, "DMG created successfully: %s\n", config.FileName)
	return nil
}

// createDMGWithSafeHardLinks создает DMG с безопасными hard links, которые не блокируются Gatekeeper
func createDMGWithSafeHardLinks(config Config, sourceDir string) error {
	// Создаем временную директорию с безопасными hard links
	safeTempDir, err := os.MkdirTemp("", "*-zapp-dmg-safe")
	if err != nil {
		return fmt.Errorf("failed to create safe temporary directory: %w", err)
	}
	defer os.RemoveAll(safeTempDir)

	// Копируем только необходимые файлы с безопасными hard links
	for _, item := range config.Contents {
		switch item.Type {
		case File:
			destPath := filepath.Join(safeTempDir, filepath.Base(item.Path))
			// Для файлов используем безопасные hard links
			if err := os.Link(item.Path, destPath); err != nil {
				// Если hard link не работает, копируем файл
				if err := copyFile(item.Path, destPath); err != nil {
					return fmt.Errorf("failed to copy file %s: %w", item.Path, err)
				}
			}
		case Dir:
			destPath := filepath.Join(safeTempDir, filepath.Base(item.Path))
			// Для директорий используем умное копирование
			if err := smartCopyAppBundle(item.Path, destPath); err != nil {
				return fmt.Errorf("failed to smart copy dir %s: %w", item.Path, err)
			}
		case Link:
			// Создаем символическую ссылку
			if err := os.Symlink(item.Path, filepath.Join(safeTempDir, filepath.Base(item.Path))); err != nil {
				return fmt.Errorf("failed to create symlink %s: %w", item.Path, err)
			}
		}
	}

	// Создаем DS_Store
	store := dsstore.NewDSStore()
	store.SetIconSize(float64(config.ContentsIconSize))
	store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
	store.SetLabelSize(float64(config.LabelSize))
	store.SetLabelPlaceToBottom(true)
	store.SetBgToDefault()
	for _, content := range config.Contents {
		store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
	}
	
	if err := store.Write(filepath.Join(safeTempDir, ".DS_Store")); err != nil {
		return fmt.Errorf("failed to write .DS_Store: %w", err)
	}

	// Копируем background если нужно
	if config.Background != "" {
		backgroundDir := filepath.Join(safeTempDir, ".background")
		if err := os.MkdirAll(backgroundDir, 0755); err != nil {
			return fmt.Errorf("failed to create .background directory: %w", err)
		}
		if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
			return fmt.Errorf("failed to copy background: %w", err)
		}
	}

	// Создаем DMG из безопасной временной директории
	ctx := context.Background()
	
	// Рассчитываем точный размер DMG
	sizeMB, err := calculateDirSize(safeTempDir)
	if err != nil {
		return fmt.Errorf("failed to calculate directory size: %w", err)
	}
	
	if config.Format == hdiutil.UDZO || config.Format == hdiutil.UDBZ {
		if err := createCompressedDMGWithSize(ctx, config, safeTempDir, sizeMB); err != nil {
			return fmt.Errorf("failed to create compressed dmg: %w", err)
		}
	} else {
		if err := hdiutil.CreateWithSize(ctx, config.Title, safeTempDir, config.Format, config.FileName, sizeMB); err != nil {
			return fmt.Errorf("failed to create dmg: %w", err)
		}
	}

	// Устанавливаем иконку если нужно
	if config.Icon != "" {
		setFileIcon(config.FileName, config.Icon)
	}

	return nil
}

// createCompressedDMG creates a compressed DMG file directly
func createCompressedDMG(ctx context.Context, config Config, sourceDir string) error {
	// Create temporary read-write DMG first
	tempDMG := filepath.Join(filepath.Dir(config.FileName), fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), filepath.Base(config.FileName)))
	defer os.Remove(tempDMG) // Clean up temp file

	// Create read-write DMG
	if err := hdiutil.Create(ctx, config.Title, sourceDir, hdiutil.UDRW, tempDMG); err != nil {
		return fmt.Errorf("failed to create temp dmg: %w", err)
	}

	// Apply customizations (icon and background) to the read-write DMG
	if config.Icon != "" || config.Background != "" {
		err := tmpMount(tempDMG, func(dmgFilePath string, mountPoint string) error {
			if config.Icon != "" {
				if err := setDMGIcon(mountPoint, config.Icon); err != nil {
					return fmt.Errorf("failed to set DMG icon: %w", err)
				}
			}
			if config.Background != "" {
				// Copy background image to mounted volume
				backgroundDir := filepath.Join(mountPoint, ".background")
				if err := os.MkdirAll(backgroundDir, 0755); err != nil {
					return fmt.Errorf("failed to create .background directory: %w", err)
				}
				if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
					return fmt.Errorf("failed to copy background: %w", err)
				}
				
				// Create DS_Store with background settings
				store := dsstore.NewDSStore()
				store.SetIconSize(float64(config.ContentsIconSize))
				store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
				store.SetLabelSize(float64(config.LabelSize))
				store.SetLabelPlaceToBottom(true)
				store.SetBgToDefault()
				store.SetBackgroundImage(filepath.Join(mountPoint, ".background", "background.png"))
				
				for _, content := range config.Contents {
					store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
				}
				
				if err := store.Write(filepath.Join(mountPoint, ".DS_Store")); err != nil {
					return fmt.Errorf("failed to write .DS_Store: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	// Ensure the DMG is properly detached before conversion
	forceDetachDMG(ctx, tempDMG)

	// Convert to compressed format
	var convertArgs []string
	if config.CompressionLevel != "" {
		convertArgs = append(convertArgs, "-imagekey", fmt.Sprintf("zlib-level=%s", config.CompressionLevel))
	}
	
	cmd := exec.CommandContext(ctx, "hdiutil", append([]string{"convert", tempDMG, "-format", string(config.Format), "-o", config.FileName}, convertArgs...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil convert failed: %w, output: %s", err, string(output))
	}

	return nil
}

func setFileIcon(dmgPath, iconPath string) error {
	// Create temporary mount point
	tempDir, err := os.MkdirTemp("", "*-zapp-dmg")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir) // Ensure cleanup of temp directory

	tempIconPath := filepath.Join(tempDir, "icon.icns")
	err = copyFile(iconPath, tempIconPath)
	if err != nil {
		return fmt.Errorf("failed to copy icon: %w", err)
	}

	cmd := exec.Command("sips", "-i", tempIconPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set icon: %s, output: %s", err, string(output))
	}
	cmd = exec.Command("DeRez", "-only", "icns", tempIconPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to DeRez icon: %s, output: %s", err, string(output))
	}
	rsrcPath := filepath.Join(tempDir, "icns.rsrc")
	if err := os.WriteFile(rsrcPath, output, 0644); err != nil {
		return fmt.Errorf("failed to write icns.rsrc: %w", err)
	}
	cmd = exec.Command("Rez", "-append", rsrcPath, "-o", dmgPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to append icns.rsrc: %s, output: %s", err, string(output))
	}
	cmd = exec.Command("SetFile", "-a", "C", dmgPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set icon: %s, output: %s", err, string(output))
	}
	return nil
}

func tmpMount(dmgPath string, process func(dmgFilePath string, mountPoint string) error) error {
	// Create temporary mount point
	tempDir, err := os.MkdirTemp("", "*-zapp-dmg")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	mountPoint := filepath.Join(tempDir, "mount")
	ctx := context.Background()
	if err = hdiutil.Attach(ctx, dmgPath, mountPoint); err != nil {
		return fmt.Errorf("failed to attach DMG: %w", err)
	}
	
	// Ensure DMG is detached even if process function panics
	defer func() {
		if detachErr := hdiutil.Detach(ctx, mountPoint); detachErr != nil {
			fmt.Printf("Warning: failed to detach DMG: %s\n", detachErr)
			// Try force detach
			forceDetachDMG(ctx, dmgPath)
		}
	}()
	
	return process(dmgPath, mountPoint)
}

func setDMGIcon(mountPoint, iconPath string) error {
	// Copy the icon to the mount point
	iconFile := filepath.Join(mountPoint, ".VolumeIcon.icns")
	if err := copyFile(iconPath, iconFile); err != nil {
		return fmt.Errorf("failed to copy icon to mount point: %w", err)
	}

	// Set the icon
	cmd := exec.Command("SetFile", "-c", "icnC", iconFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set icon: %s, output: %s", err, string(output))
	}

	// Tell the volume that it has a special file attribute
	cmd = exec.Command("SetFile", "-a", "C", mountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set icon: %s, output: %s", err, string(output))
	}

	return nil
}

// setupSourceDirectory sets up the source directory with the necessary files.
func setupSourceDirectory(config Config, sourceDir string) error {
	// Optimize app bundles before copying
	if config.OptimizeAppSize {
		for _, item := range config.Contents {
			if item.Type == Dir && strings.HasSuffix(item.Path, ".app") {
				if err := optimizeAppBundle(item.Path); err != nil {
					// Log warning but continue
					if config.LogWriter != nil {
						fmt.Fprintf(config.LogWriter, "Warning: failed to optimize app bundle %s: %v\n", item.Path, err)
					}
				}
			}
		}
	}

	// Copy the application and other files to the source directory
	for _, item := range config.Contents {
		switch item.Type {

		case File:
			// Copy the file to the source directory
			destPath := filepath.Join(sourceDir, filepath.Base(item.Path))
			if config.UseHardLinks {
				// Try to create hard link first, fall back to copy if it fails
				if err := os.Link(item.Path, destPath); err != nil {
					// Fall back to regular copy
					if err := copyFile(item.Path, destPath); err != nil {
						return fmt.Errorf("failed to copy file %s to %s: %s", item.Path, destPath, err)
					}
				}
			} else {
				if err := copyFile(item.Path, destPath); err != nil {
					return fmt.Errorf("failed to copy file %s to %s: %s", item.Path, destPath, err)
				}
			}
		case Dir:
			// Copy the file to the source directory
			destPath := filepath.Join(sourceDir, filepath.Base(item.Path))
			if config.UseHardLinks {
				// Try to create hard link copy first, fall back to regular copy if it fails
				if err := copyDirWithHardLinks(item.Path, destPath); err != nil {
					// Fall back to regular copy
					if err := copyDir(item.Path, destPath); err != nil {
						return fmt.Errorf("failed to copy dir %s to %s: %s", item.Path, destPath, err)
					}
				}
			} else {
				// Use smart copy for .app bundles to avoid copying unnecessary files
				if strings.HasSuffix(item.Path, ".app") {
					if err := smartCopyAppBundle(item.Path, destPath); err != nil {
						return fmt.Errorf("failed to smart copy app bundle %s to %s: %s", item.Path, destPath, err)
					}
				} else {
					if err := copyDir(item.Path, destPath); err != nil {
						return fmt.Errorf("failed to copy dir %s to %s: %s", item.Path, destPath, err)
					}
				}
			}
		case Link:
			// Create a symbolic link
			err := os.Symlink(item.Path, filepath.Join(sourceDir, filepath.Base(item.Path)))
			if err != nil {
				return fmt.Errorf("failed to create symbolic link %s: %s", item.Path, err)
			}
		}
	}

	// 배경 이미지 복사
	if config.Background != "" {
		backgroundDir := filepath.Join(sourceDir, ".background")
		if err := os.MkdirAll(backgroundDir, 0755); err != nil {
			return fmt.Errorf("failed to create .background directory: %w", err)
		}
		if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
			return fmt.Errorf("failed to copy background: %s", err)
		}
	}
	return nil
}

func isDir(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	// handle symbol link
	if fi.Mode()&os.ModeSymlink != 0 {
		realPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false, err
		}
		fi, err = os.Stat(realPath)
		if err != nil {
			return false, err
		}
	}

	return fi.IsDir(), nil
}

// copyDir copies a directory from src to dst recursively.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		entryPath := srcPath
		is_dir, err := isDir(entryPath)
		if err != nil {
			continue
		}

		if is_dir {
			if err = copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err = copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	
	// Use buffered copy for better performance
	buffer := make([]byte, 32*1024) // 32KB buffer
	_, err = io.CopyBuffer(dstFile, srcFile, buffer)
	return err
}

// copyDirWithHardLinks copies a directory from src to dst recursively using hard links where possible.
func copyDirWithHardLinks(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		entryPath := srcPath
		is_dir, err := isDir(entryPath)
		if err != nil {
			continue
		}

		if is_dir {
			if err = copyDirWithHardLinks(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Try to create hard link first, fall back to copy if it fails
			if err := os.Link(srcPath, dstPath); err != nil {
				// Fall back to regular copy
				if err := copyFile(srcPath, dstPath); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// forceDetachDMG ensures a DMG file is properly detached
func forceDetachDMG(ctx context.Context, dmgPath string) {
	// Try to detach using hdiutil info to find mounted volumes
	cmd := exec.CommandContext(ctx, "hdiutil", "info")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return
	}

	// Parse output to find mounted volumes from this DMG
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, dmgPath) && strings.Contains(line, "/Volumes/") {
			// Extract mount point
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				mountPoint := parts[2]
				// Try to detach this mount point
				hdiutil.Detach(ctx, mountPoint)
			}
		}
	}
}

// optimizeAppBundle removes unnecessary files from the app bundle to reduce size
func optimizeAppBundle(appPath string) error {
	// Remove unnecessary files that increase DMG size
	filesToRemove := []string{
		filepath.Join(appPath, "Contents", "_CodeSignature", "CodeResources"), // Will be recreated during signing
		filepath.Join(appPath, ".DS_Store"),
		filepath.Join(appPath, "Contents", ".DS_Store"),
	}

	for _, file := range filesToRemove {
		if _, err := os.Stat(file); err == nil {
			os.Remove(file)
		}
	}

	// Clean up any temporary files in the app bundle
	tempPatterns := []string{
		filepath.Join(appPath, "Contents", "MacOS", "*.tmp"),
		filepath.Join(appPath, "Contents", "Resources", "*.tmp"),
		filepath.Join(appPath, "Contents", "Frameworks", "*.tmp"),
	}

	for _, pattern := range tempPatterns {
		matches, _ := filepath.Glob(pattern)
		for _, match := range matches {
			os.Remove(match)
		}
	}

	return nil
}

// smartCopyAppBundle копирует только необходимые файлы из .app bundle, исключая временные и системные файлы
func smartCopyAppBundle(src, dst string) error {
	// Используем надежную копию директории, но с фильтрацией ненужных файлов
	return smartCopyDir(src, dst)
}

// smartCopyDir копирует директорию с фильтрацией ненужных файлов
func smartCopyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	// Список файлов и папок, которые нужно исключить из копирования
	excludePatterns := []string{
		".DS_Store",
		"__MACOSX",
		".Trashes",
		".fseventsd",
		".Spotlight-V100",
		".TemporaryItems",
		"*.tmp",
		"*.log",
		"*.cache",
	}

	// Функция для проверки, нужно ли исключить файл
	shouldExclude := func(name string) bool {
		for _, pattern := range excludePatterns {
			matched, _ := filepath.Match(pattern, name)
			if matched {
				return true
			}
		}
		return false
	}

	for _, entry := range entries {
		// Пропускаем исключенные файлы и папки
		if shouldExclude(entry.Name()) {
			continue
		}

		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		entryPath := srcPath
		is_dir, err := isDir(entryPath)
		if err != nil {
			continue
		}

		if is_dir {
			if err = smartCopyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err = copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// createSymbolicLinks creates symbolic links to original files instead of copying
func createSymbolicLinks(config Config, tempDir string) error {
	for _, item := range config.Contents {
		switch item.Type {
		case File:
			// Create symbolic link to the original file
			destPath := filepath.Join(tempDir, filepath.Base(item.Path))
			if err := os.Symlink(item.Path, destPath); err != nil {
				return fmt.Errorf("failed to create symbolic link for file %s: %w", item.Path, err)
			}
		case Dir:
			// For directories, create symbolic link to the original directory
			destPath := filepath.Join(tempDir, filepath.Base(item.Path))
			if err := os.Symlink(item.Path, destPath); err != nil {
				return fmt.Errorf("failed to create symbolic link for directory %s: %w", item.Path, err)
			}
		case Link:
			// Create a symbolic link
			err := os.Symlink(item.Path, filepath.Join(tempDir, filepath.Base(item.Path)))
			if err != nil {
				return fmt.Errorf("failed to create symbolic link %s: %w", item.Path, err)
			}
		}
	}

	// Copy background image (this needs to be a real file, not a link)
	if config.Background != "" {
		backgroundDir := filepath.Join(tempDir, ".background")
		if err := os.MkdirAll(backgroundDir, 0755); err != nil {
			return fmt.Errorf("failed to create .background directory: %w", err)
		}
		if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
			return fmt.Errorf("failed to copy background: %w", err)
		}
	}

	return nil
}

// createCompressedDMGDirect creates a compressed DMG file directly using symbolic links
func createCompressedDMGDirect(ctx context.Context, config Config, tempDir string) error {
	// Create temporary read-write DMG first
	tempDMG := filepath.Join(filepath.Dir(config.FileName), fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), filepath.Base(config.FileName)))
	defer os.Remove(tempDMG) // Clean up temp file

	// Create read-write DMG
	if err := hdiutil.Create(ctx, config.Title, tempDir, hdiutil.UDRW, tempDMG); err != nil {
		return fmt.Errorf("failed to create temp dmg: %w", err)
	}

	// Apply customizations (icon and background) to the read-write DMG
	if config.Icon != "" || config.Background != "" {
		err := tmpMount(tempDMG, func(dmgFilePath string, mountPoint string) error {
			if config.Icon != "" {
				if err := setDMGIcon(mountPoint, config.Icon); err != nil {
					return fmt.Errorf("failed to set DMG icon: %w", err)
				}
			}
			if config.Background != "" {
				// Copy background image to mounted volume
				backgroundDir := filepath.Join(mountPoint, ".background")
				if err := os.MkdirAll(backgroundDir, 0755); err != nil {
					return fmt.Errorf("failed to create .background directory: %w", err)
				}
				if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
					return fmt.Errorf("failed to copy background: %w", err)
				}
				
				// Create DS_Store with background settings
				store := dsstore.NewDSStore()
				store.SetIconSize(float64(config.ContentsIconSize))
				store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
				store.SetLabelSize(float64(config.LabelSize))
				store.SetLabelPlaceToBottom(true)
				store.SetBgToDefault()
				store.SetBackgroundImage(filepath.Join(mountPoint, ".background", "background.png"))
				
				for _, content := range config.Contents {
					store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
				}
				
				if err := store.Write(filepath.Join(mountPoint, ".DS_Store")); err != nil {
					return fmt.Errorf("failed to write .DS_Store: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	// Ensure the DMG is properly detached before conversion
	forceDetachDMG(ctx, tempDMG)

	// Convert to compressed format
	var convertArgs []string
	if config.CompressionLevel != "" {
		convertArgs = append(convertArgs, "-imagekey", fmt.Sprintf("zlib-level=%s", config.CompressionLevel))
	}
	
	cmd := exec.CommandContext(ctx, "hdiutil", append([]string{"convert", tempDMG, "-format", string(config.Format), "-o", config.FileName}, convertArgs...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil convert failed: %w, output: %s", err, string(output))
	}

	return nil
}

// calculateDirSize вычисляет общий размер директории в мегабайтах
func calculateDirSize(dir string) (int, error) {
	var totalSize int64
	
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	
	if err != nil {
		return 0, err
	}
	
	// Конвертируем в мегабайты и добавляем 20% запаса
	sizeMB := int(totalSize/(1024*1024)) + int(float64(totalSize/(1024*1024))*0.2)
	
	// Минимальный размер 100MB
	if sizeMB < 100 {
		sizeMB = 100
	}
	
	return sizeMB, nil
}

// createCompressedDMGWithSize создает сжатый DMG файл с точным контролем размера
func createCompressedDMGWithSize(ctx context.Context, config Config, sourceDir string, sizeMB int) error {
	// Create temporary read-write DMG first with exact size
	tempDMG := filepath.Join(filepath.Dir(config.FileName), fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), filepath.Base(config.FileName)))
	defer os.Remove(tempDMG) // Clean up temp file

	// Create read-write DMG with exact size
	if err := hdiutil.CreateWithSize(ctx, config.Title, sourceDir, hdiutil.UDRW, tempDMG, sizeMB); err != nil {
		return fmt.Errorf("failed to create temp dmg: %w", err)
	}

	// Apply customizations (icon and background) to the read-write DMG
	if config.Icon != "" || config.Background != "" {
		err := tmpMount(tempDMG, func(dmgFilePath string, mountPoint string) error {
			if config.Icon != "" {
				if err := setDMGIcon(mountPoint, config.Icon); err != nil {
					return fmt.Errorf("failed to set DMG icon: %w", err)
				}
			}
			if config.Background != "" {
				// Copy background image to mounted volume
				backgroundDir := filepath.Join(mountPoint, ".background")
				if err := os.MkdirAll(backgroundDir, 0755); err != nil {
					return fmt.Errorf("failed to create .background directory: %w", err)
				}
				if err := copyFile(config.Background, filepath.Join(backgroundDir, "background.png")); err != nil {
					return fmt.Errorf("failed to copy background: %w", err)
				}
				
				// Create DS_Store with background settings
				store := dsstore.NewDSStore()
				store.SetIconSize(float64(config.ContentsIconSize))
				store.SetWindow(config.WindowWidth, config.WindowHeight, 0, 0)
				store.SetLabelSize(float64(config.LabelSize))
				store.SetLabelPlaceToBottom(true)
				store.SetBgToDefault()
				store.SetBackgroundImage(filepath.Join(mountPoint, ".background", "background.png"))
				
				for _, content := range config.Contents {
					store.SetIconPos(filepath.Base(content.Path), uint32(content.X), uint32(content.Y))
				}
				
				if err := store.Write(filepath.Join(mountPoint, ".DS_Store")); err != nil {
					return fmt.Errorf("failed to write .DS_Store: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	// Ensure the DMG is properly detached before conversion
	forceDetachDMG(ctx, tempDMG)

	// Convert to compressed format
	var convertArgs []string
	if config.CompressionLevel != "" {
		convertArgs = append(convertArgs, "-imagekey", fmt.Sprintf("zlib-level=%s", config.CompressionLevel))
	}
	
	cmd := exec.CommandContext(ctx, "hdiutil", append([]string{"convert", tempDMG, "-format", string(config.Format), "-o", config.FileName}, convertArgs...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil convert failed: %w, output: %s", err, string(output))
	}

	return nil
}
