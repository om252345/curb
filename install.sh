#!/bin/bash
set -e

# Curb Installation Script
# Downloads the appropriate binaries for your system from GitHub Releases.

REPO="om252345/curb" # Replace with your actual repo
GITHUB_URL="https://github.com/$REPO/releases/download"

# Detect OS and Architecture
OS="linux"
if [[ "$OSTYPE" == "darwin"* ]]; then
    OS="darwin"
elif [[ "$OSTYPE" == "msys" || "$OSTYPE" == "win32" ]]; then
    OS="windows"
fi

ARCH="amd64"
if [[ "$(uname -m)" == "arm64" || "$(uname -m)" == "aarch64" ]]; then
    ARCH="arm64"
fi

# Determine latest version if not provided
if [ -z "$VERSION" ]; then
    VERSION=$(curl -s https://api.github.com/repos/$REPO/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
fi

if [ -z "$VERSION" ]; then
    echo "❌ Error: Could not determine latest version."
    exit 1
fi

echo "🚀 Installing Curb $VERSION for $OS-$ARCH..."

# Setup directories
CURB_HOME="$HOME/.curb"
CURB_BIN="$CURB_HOME/bin"
mkdir -p "$CURB_BIN"

# Download and extract
TMP_DIR=$(mktemp -d)
FILENAME="curb-backend_${VERSION#v}_${OS^}_${ARCH/amd64/x86_64}.tar.gz"
if [[ "$OS" == "windows" ]]; then
    FILENAME="${FILENAME%.tar.gz}.zip"
fi

URL="$GITHUB_URL/$VERSION/$FILENAME"

echo "📥 Downloading from $URL..."
curl -L "$URL" -o "$TMP_DIR/$FILENAME"

if [[ "$FILENAME" == *.tar.gz ]]; then
    tar -xzf "$TMP_DIR/$FILENAME" -C "$TMP_DIR"
else
    unzip "$TMP_DIR/$FILENAME" -d "$TMP_DIR"
fi

# Move binaries
mv "$TMP_DIR/curb" "$CURB_BIN/"
mv "$TMP_DIR/curb-interceptor" "$CURB_BIN/"
mv "$TMP_DIR/curb-mcp" "$CURB_BIN/"
chmod +x "$CURB_BIN/"*

# Cleanup
rm -rf "$TMP_DIR"

# PATH check - Core binaries go in ~/.curb/bin safely
PATH_LINE="export PATH=\"\$HOME/.curb/bin:\$PATH\""
SHELL_RC=""

if [[ "$SHELL" == *"zsh"* ]]; then
    SHELL_RC="$HOME/.zshrc"
elif [[ "$SHELL" == *"bash"* ]]; then
    SHELL_RC="$HOME/.bashrc"
fi

# On macOS/Linux, inject PATH into shell profile
if [ -n "$SHELL_RC" ]; then
    if ! grep -q ".curb/bin" "$SHELL_RC"; then
        echo "📝 Adding Curb to PATH in $SHELL_RC"
        echo "" >> "$SHELL_RC"
        echo "# Curb Security Mesh" >> "$SHELL_RC"
        echo "$PATH_LINE" >> "$SHELL_RC"
        echo "👉 Please restart your terminal or run: source $SHELL_RC"
    fi
elif [[ "$OS" == "windows" ]]; then
    echo "📝 To use curb globally on Windows, please add $CURB_BIN to your System PATH."
fi


echo "✅ Curb installed successfully!"
echo "👉 Run 'curb --version' to verify."
