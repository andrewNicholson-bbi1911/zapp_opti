package dmg

import (
	"fmt"
	"github.com/ironpark/zapp/cmd"
	"github.com/ironpark/zapp/pkg/mactools/dmg"
	"os"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/urfave/cli/v2"
	"github.com/ironpark/zapp/pkg/mactools/hdiutil"
)

//go:embed iconfile.icns
var defaultIconFile []byte

// flags
var (
	appDir                    string
	out                       string
	title                     string
	icon                      string
	background                string
	windowWidth, windowHeight int
	labelSize                 int
	contentsIconSize          int
	dmgFormat                 string
	compressionLevel          string
	useHardLinks              bool
)

var Command = &cli.Command{
	Name:        "dmg",
	Usage:       "Create a .dmg for macOS application deployment",
	UsageText:   "",
	Description: "",
	Args:        true,
	ArgsUsage:   " <path of app-bundle>",
	Action: func(c *cli.Context) error {
		logger := cmd.NewAppLogger(c.App)
		// Create a temporary working directory
		tempDir, err := os.MkdirTemp("", "*-zapp-dmg")
		if err != nil {
			return fmt.Errorf("error creating temporary directory: %v", err)
		}
		defer os.RemoveAll(tempDir)

		logger.Printf("Start Creating DMG file for %s\n", filepath.Base(appDir))

		if icon == "" {
			logger.Println("Icon file not provided")
			logger.Println("Create dmg disk file icon using app icon")
			tempDirForIcon, err := os.MkdirTemp("", "*-zapp-dmg-icon")
			if err != nil {
				return fmt.Errorf("error creating temporary directory: %v", err)
			}
			defer os.RemoveAll(tempDir)
			icon = filepath.Join(tempDirForIcon, "icon.icns")
			err = createIconSet(appDir, icon, !c.Bool("use-original-icon"))
			if err != nil {
				return err
			}
		}
		if out == "" {
			out = filepath.Base(appDir)
			out = strings.TrimSuffix(out, filepath.Ext(out))
			out = out + ".dmg"
		}
		if title == "" {
			title = filepath.Base(appDir)
			title = strings.TrimSuffix(title, filepath.Ext(title))
		}

		centerY := int(float64(windowHeight)/2-float64(contentsIconSize)/2) + labelSize
		defaultConfig := dmg.Config{
			FileName:         out,
			Title:            title,
			Icon:             icon,
			LabelSize:        labelSize,
			ContentsIconSize: contentsIconSize,
			WindowWidth:      windowWidth,
			WindowHeight:     windowHeight,
			Background:       background,
			Format:           hdiutil.Format(dmgFormat),
			CompressionLevel: compressionLevel,
			UseHardLinks:     useHardLinks,
			Contents: []dmg.Item{
				{X: int(float64(windowWidth)/3*1 - float64(contentsIconSize)/2), Y: centerY, Type: dmg.Dir, Path: appDir},
				{X: int(float64(windowWidth)/3*2 + float64(contentsIconSize)/2), Y: centerY, Type: dmg.Link, Path: "/Applications"},
			},
		}
		logger.PrintValue("Title", title)
		logger.PrintValue("Icon", icon)
		logger.PrintValue("labelSize", labelSize)
		logger.PrintValue("AppPath", appDir)
		logger.PrintValue("OutputPath", out)
		logger.PrintValue("ContentsIconSize", contentsIconSize)
		logger.PrintValue("WindowWidth", windowWidth)
		logger.PrintValue("WindowHeight", windowHeight)
		logger.PrintValue("Background", background)
		logger.PrintValue("DMG Format", dmgFormat)
		logger.PrintValue("Compression Level", compressionLevel)
		logger.PrintValue("Use Hard Links", useHardLinks)
		logger.Println("Creating optimized DMG file...")
		err = dmg.CreateDMG(defaultConfig, tempDir)
		if err != nil {
			return err
		}
		logger.Success("DMG file created successfully!")
		err = cmd.RunSignCmd(c, out)
		if err != nil {
			return fmt.Errorf("failed to sign PKG: %v", err)
		}

		err = cmd.RunNotarizeCmd(c, out)
		if err != nil {
			return fmt.Errorf("failed to notarize PKG: %v", err)
		}
		return nil
	},
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:        "background",
			Usage:       "Path to the background image file",
			Aliases:     []string{"bg"},
			Destination: &background,
		},
		&cli.StringFlag{
			Name:        "title",
			Usage:       "The title displayed when the DMG file is mounted",
			Aliases:     []string{"t"},
			Destination: &title,
		},
		&cli.StringFlag{
			Name:        "app",
			Usage:       "App bundle path",
			Destination: &appDir,
			Required:    true,
			Action: func(c *cli.Context, app string) error {
				if !strings.HasSuffix(app, ".app") {
					return fmt.Errorf("not valid app bundle extension")
				}
				// Check if the app bundle path is valid
				fileInfo, err := os.Stat(app)
				if err != nil {
					return fmt.Errorf("error accessing app-bundle path: %v", err)
				}
				if !fileInfo.IsDir() {
					return fmt.Errorf("app-bundle path must be a directory")
				}
				return nil
			},
		},
		&cli.StringFlag{
			Name:        "out",
			Usage:       "The output DMG file name",
			Aliases:     []string{"o"},
			Destination: &out,
		},
		&cli.StringFlag{
			Name:        "icon",
			Usage:       "Path to the icon file to display in the DMG file (icns, png)",
			Destination: &icon,
		},
		&cli.IntFlag{
			Name:        "window-width",
			Usage:       "Width of the Finder window when the DMG file is opened",
			Aliases:     []string{"ww"},
			Destination: &windowWidth,
			Value:       640,
		},
		&cli.IntFlag{
			Name:        "window-height",
			Usage:       "Height of the Finder window when the DMG file is opened",
			Aliases:     []string{"wh"},
			Destination: &windowHeight,
			Value:       480,
		},
		&cli.IntFlag{
			Name:        "label-size",
			Usage:       "Size of the label text in the Finder window (10-16)",
			Aliases:     []string{"ls"},
			Destination: &labelSize,
			Value:       14,
			Action: func(*cli.Context, int) error {
				if labelSize < 10 || labelSize > 16 {
					return fmt.Errorf("label-size must be between 10 and 16")
				}
				return nil
			},
		},
		&cli.IntFlag{
			Name:        "contents-icon-size",
			Usage:       "Size of the icons in the Finder window (16-512)",
			Aliases:     []string{"cis"},
			Destination: &contentsIconSize,
			Value:       128,
			Action: func(*cli.Context, int) error {
				if contentsIconSize < 16 || contentsIconSize > 512 {
					return fmt.Errorf("contents-icon-size must be between 16 and 512")
				}
				return nil
			},
		},
		&cli.BoolFlag{
			Name:    "use-original-icon ",
			Aliases: []string{"uoi"},
			Usage:   "Use the original icon file without modifications.",
		},
		&cli.StringFlag{
			Name:        "format",
			Usage:       "DMG format (UDRO, UDRW, UDZO, UDBZ) - UDZO recommended for best compression",
			Aliases:     []string{"f"},
			Destination: &dmgFormat,
			Value:       "UDZO",
			Action: func(c *cli.Context, format string) error {
				validFormats := map[string]bool{
					"UDRO": true,
					"UDRW": true,
					"UDZO": true,
					"UDBZ": true,
				}
				if !validFormats[format] {
					return fmt.Errorf("invalid format: %s. Valid formats: UDRO, UDRW, UDZO, UDBZ", format)
				}
				return nil
			},
		},
		&cli.StringFlag{
			Name:        "compression-level",
			Usage:       "Compression level for UDZO format (1-9, where 9 is best compression)",
			Aliases:     []string{"cl"},
			Destination: &compressionLevel,
			Value:       "6",
			Action: func(c *cli.Context, level string) error {
				if level != "" {
					validLevels := map[string]bool{
						"1": true, "2": true, "3": true, "4": true, "5": true,
						"6": true, "7": true, "8": true, "9": true,
					}
					if !validLevels[level] {
						return fmt.Errorf("invalid compression level: %s. Must be between 1-9", level)
					}
				}
				return nil
			},
		},
		&cli.BoolFlag{
			Name:        "use-hard-links",
			Usage:       "Use hard links instead of copying files (reduces temporary disk usage)",
			Aliases:     []string{"hl"},
			Destination: &useHardLinks,
			Value:       false,
		},
	}, cmd.CreateSubTaskFlags()...),
	HelpName:           "",
	CustomHelpTemplate: "",
}
