#!/bin/bash

# Тестовый скрипт для проверки оптимизированной сборки DMG

echo "=== Тестирование оптимизированной сборки DMG ==="

# Проверяем наличие тестового приложения
TEST_APP="TestApp.app"
if [ ! -d "$TEST_APP" ]; then
    echo "Создаем тестовое приложение..."
    mkdir -p "$TEST_APP/Contents/MacOS"
    mkdir -p "$TEST_APP/Contents/Resources"
    
    # Создаем минимальный Info.plist
    cat > "$TEST_APP/Contents/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>TestApp</string>
    <key>CFBundleIdentifier</key>
    <string>com.example.TestApp</string>
    <key>CFBundleName</key>
    <string>TestApp</string>
    <key>CFBundleVersion</key>
    <string>1.0</string>
</dict>
</plist>
EOF
    
    # Создаем минимальный исполняемый файл
    cat > "$TEST_APP/Contents/MacOS/TestApp" << 'EOF'
#!/bin/bash
echo "Test Application"
sleep 1
EOF
    chmod +x "$TEST_APP/Contents/MacOS/TestApp"
    
    echo "Тестовое приложение создано: $TEST_APP"
fi

echo ""
echo "1. Тестируем создание DMG с форматом UDZO (рекомендуемый)..."
./zapp_fixed dmg --app "$TEST_APP" --format UDZO --compression-level 6 --out "test_udzo.dmg"

if [ $? -eq 0 ]; then
    echo "✅ UDZO DMG создан успешно"
    ls -lh "test_udzo.dmg"
else
    echo "❌ Ошибка при создании UDZO DMG"
fi

echo ""
echo "2. Тестируем создание DMG с форматом UDBZ (максимальное сжатие)..."
./zapp_fixed dmg --app "$TEST_APP" --format UDBZ --out "test_udbz.dmg"

if [ $? -eq 0 ]; then
    echo "✅ UDBZ DMG создан успешно"
    ls -lh "test_udbz.dmg"
else
    echo "❌ Ошибка при создании UDBZ DMG"
fi

echo ""
echo "3. Тестируем создание DMG с hard links..."
./zapp_fixed dmg --app "$TEST_APP" --format UDZO --use-hard-links --out "test_hardlinks.dmg"

if [ $? -eq 0 ]; then
    echo "✅ DMG с hard links создан успешно"
    ls -lh "test_hardlinks.dmg"
else
    echo "❌ Ошибка при создании DMG с hard links"
fi

echo ""
echo "=== Сравнение размеров ==="
for file in test_*.dmg; do
    if [ -f "$file" ]; then
        size=$(stat -f%z "$file" 2>/dev/null || stat -c%s "$file" 2>/dev/null)
        size_mb=$((size / 1024 / 1024))
        echo "$file: ${size_mb}MB"
    fi
done

echo ""
echo "=== Очистка тестовых файлов ==="
rm -f test_*.dmg
rm -rf "$TEST_APP"

echo "Тестирование завершено!"
