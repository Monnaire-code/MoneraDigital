# 🌐 Agent Browser - Global Installation Guide

**Status:** ✅ **INSTALLED AND CONFIGURED**
**Date:** 2026-01-16
**Installation Location:** `/opt/homebrew/bin/agent-browser`
**Configuration:** `~/.config/claude-code/agent-browser-config.env`

---

## 📋 Installation Summary

Agent Browser has been successfully installed globally and configured for use in any terminal and Claude Code.

### What Was Installed

```
╔════════════════════════════════════════════════════════════════╗
║                    Installation Summary                       ║
╠════════════════════════════════════════════════════════════════╣
║                                                                ║
║ Tool:            Agent Browser                                ║
║ Version:         0.5.0 (latest)                               ║
║ Installation:    Global (via npm)                             ║
║ Executable Path: /opt/homebrew/bin/agent-browser              ║
║ Configuration:   ~/.config/claude-code/                       ║
║ Status:          ✅ Ready to Use                              ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝
```

---

## 🚀 Quick Start

### Verify Installation

```bash
# Check if agent-browser is available
which agent-browser

# Expected output:
# /opt/homebrew/bin/agent-browser

# Show help
agent-browser --help

# Show available commands
agent-browser help
```

### Basic Usage Examples

```bash
# Open a URL
agent-browser open https://example.com

# Take a screenshot
agent-browser screenshot

# Click an element
agent-browser click ".button-class"

# Type text
agent-browser type "#input-id" "hello world"

# Scroll page
agent-browser scroll down 500

# Get page content
agent-browser get text
```

---

## 🔧 Configuration Details

### Configuration File Location

```
~/.config/claude-code/agent-browser-config.env
```

### Configuration Variables

```bash
# Whether agent-browser is installed
AGENT_BROWSER_INSTALLED=true

# Path to agent-browser executable
AGENT_BROWSER_PATH=/opt/homebrew/bin/agent-browser

# Run in headless mode
AGENT_BROWSER_HEADLESS=true

# Default timeout for operations (ms)
AGENT_BROWSER_TIMEOUT=30000

# Optional: Enable debug logging
# AGENT_BROWSER_DEBUG=true
```

### Shell Integration

The setup automatically adds Agent Browser to your shell configuration:

- **For Zsh:** `~/.zshrc`
- **For Bash:** `~/.bash_profile`

The configuration is automatically sourced when you open a new terminal.

---

## 📱 Usage in Claude Code

### Direct Use

Agent Browser is now available in any Claude Code task without installation:

```typescript
// In Claude Code, you can now use agent-browser commands
// Example: Testing a web application
const { execSync } = require('child_process');

// Open browser
execSync('agent-browser open http://localhost:3000');

// Take screenshot
execSync('agent-browser screenshot ./screenshot.png');

// Interact with page
execSync('agent-browser click "[role=button]"');
```

### With Task Tool

You can use Agent Browser in any task automation:

```bash
# Navigate and test
agent-browser open http://localhost:5000/dashboard/lending
agent-browser wait 2000
agent-browser screenshot lending-page.png
agent-browser click "button[aria-label='Apply']"
agent-browser type "input[type='number']" "100"
agent-browser click "button[type='submit']"
```

---

## 🧪 Testing & Verification

### Test 1: Basic Functionality

```bash
# Open example.com
agent-browser open https://example.com

# Verify page loaded
agent-browser get url

# Take screenshot
agent-browser screenshot test-screenshot.png

# Expected: Screenshot saved successfully
```

### Test 2: Element Interaction

```bash
# Open test page
agent-browser open https://example.com

# Find element
agent-browser find text "Example"

# Get element count
agent-browser get count "h1"

# Expected: Returns element count
```

### Test 3: JavaScript Execution

```bash
# Execute JavaScript
agent-browser eval "document.title"

# Expected: Returns page title
```

---

## 📊 Installed Commands

Agent Browser comes with comprehensive CLI commands:

### Navigation
```bash
agent-browser open <url>           # Navigate to URL
agent-browser back                 # Go back
agent-browser forward              # Go forward
agent-browser reload               # Reload page
```

### Element Interaction
```bash
agent-browser click <selector>     # Click element
agent-browser type <selector> <text>  # Type text
agent-browser fill <selector> <text>  # Clear and fill
agent-browser hover <selector>     # Hover element
agent-browser scroll <direction>   # Scroll page
```

### Information Retrieval
```bash
agent-browser screenshot [path]    # Take screenshot
agent-browser get text [selector]  # Get element text
agent-browser get html [selector]  # Get element HTML
agent-browser get url              # Get current URL
agent-browser snapshot             # Get accessibility tree
```

### Utilities
```bash
agent-browser wait <selector|ms>   # Wait for element
agent-browser eval <js>            # Run JavaScript
agent-browser close                # Close browser
```

---

## 🔐 Environment Variables

### Global Environment Variables

Agent Browser respects these environment variables:

```bash
# Browser settings
AGENT_BROWSER_HEADLESS=true        # Run headless (no GUI)
AGENT_BROWSER_TIMEOUT=30000        # Default timeout (ms)
AGENT_BROWSER_VIEWPORT=1920,1080   # Viewport size

# Debug settings
AGENT_BROWSER_DEBUG=true           # Enable debug logging
AGENT_BROWSER_VERBOSE=true         # Verbose output

# Path settings
AGENT_BROWSER_DOWNLOADS=/path      # Download directory
AGENT_BROWSER_TEMP=/tmp            # Temporary directory
```

### Setting Environment Variables

```bash
# In current session
export AGENT_BROWSER_HEADLESS=false

# Permanently (add to ~/.zshrc or ~/.bash_profile)
echo 'export AGENT_BROWSER_HEADLESS=true' >> ~/.zshrc
source ~/.zshrc
```

---

## 🎯 Use Cases

### 1. Testing Web Applications

```bash
#!/bin/bash
# Test lending page
agent-browser open http://localhost:5000/dashboard/lending
agent-browser wait "table"
agent-browser screenshot lending-table.png
agent-browser get count "tbody tr"  # Count rows
```

### 2. UI Automation

```bash
#!/bin/bash
# Fill and submit form
agent-browser open http://localhost:5000/register
agent-browser type "[name='email']" "user@example.com"
agent-browser type "[name='password']" "secure-password"
agent-browser click "[type='submit']"
agent-browser wait ".success-message"
```

### 3. Accessibility Testing

```bash
#!/bin/bash
# Check accessibility
agent-browser open http://localhost:5000/dashboard
agent-browser snapshot  # Returns accessibility tree
agent-browser set media light  # Test light mode
agent-browser set media dark   # Test dark mode
```

### 4. Visual Testing

```bash
#!/bin/bash
# Take screenshots for visual testing
agent-browser open http://localhost:5000/dashboard/lending
agent-browser screenshot desktop.png

# Mobile viewport
agent-browser set viewport 375 812  # iPhone size
agent-browser screenshot mobile.png

# Tablet viewport
agent-browser set viewport 768 1024  # iPad size
agent-browser screenshot tablet.png
```

---

## 🛠️ Troubleshooting

### Issue 1: agent-browser not found in new terminal

**Solution:**
```bash
# Reload shell configuration
source ~/.zshrc  # or ~/.bash_profile

# Verify installation
which agent-browser
```

### Issue 2: Permission denied

**Solution:**
```bash
# Ensure executable permission
chmod +x /opt/homebrew/bin/agent-browser

# Verify
ls -l /opt/homebrew/bin/agent-browser
```

### Issue 3: Browser won't open

**Solution:**
```bash
# Check if headless mode is enabled
echo $AGENT_BROWSER_HEADLESS

# Run without headless mode for debugging
AGENT_BROWSER_HEADLESS=false agent-browser open https://example.com
```

### Issue 4: Timeout errors

**Solution:**
```bash
# Increase default timeout
export AGENT_BROWSER_TIMEOUT=60000  # 60 seconds

# Wait explicitly
agent-browser wait 5000  # Wait 5 seconds
```

---

## 📚 Advanced Features

### Custom Viewport

```bash
# Set custom viewport size
agent-browser set viewport 1280 720

# iPhone 12 size
agent-browser set viewport 390 844

# iPad size
agent-browser set viewport 768 1024
```

### Device Emulation

```bash
# Emulate specific device
agent-browser set device "iPhone 12"

# Available devices: iPhone, iPad, Android, etc.
```

### Network Simulation

```bash
# Go offline
agent-browser set offline on

# Go online
agent-browser set offline off

# Set custom headers
agent-browser set headers '{"Authorization":"Bearer token"}'
```

### Geolocation

```bash
# Set geolocation
agent-browser set geo 37.7749 -122.4194  # San Francisco
```

---

## 🔍 Integration with Claude Code

### In Claude Code Tasks

Agent Browser is automatically available in Claude Code tasks. You can use it directly:

```bash
# Inside a Claude Code task
agent-browser open http://localhost:5000
agent-browser screenshot page.png
agent-browser get text "h1"
```

### In Node.js/JavaScript

```javascript
const { execSync } = require('child_process');

// Use agent-browser in Node.js
const output = execSync('agent-browser get url').toString();
console.log('Current URL:', output);
```

### In Python

```python
import subprocess

# Use agent-browser in Python
result = subprocess.run(['agent-browser', 'open', 'https://example.com'],
                       capture_output=True, text=True)
print(result.stdout)
```

---

## 🔄 Updating Agent Browser

To update to the latest version:

```bash
# Update via npm
npm install -g agent-browser@latest

# Or via Homebrew
brew upgrade agent-browser

# Verify update
agent-browser --help
```

---

## 📖 Documentation & Resources

### Official Documentation
- **GitHub:** https://github.com/vercel-labs/agent-browser
- **Command Help:** `agent-browser --help`
- **Interactive Help:** `agent-browser help <command>`

### Quick Reference

```bash
# Get help on specific command
agent-browser help open
agent-browser help click
agent-browser help set

# List all available commands
agent-browser help
```

---

## ✅ Verification Checklist

- ✅ Agent Browser installed globally
- ✅ Available in PATH: `/opt/homebrew/bin/agent-browser`
- ✅ Configuration created: `~/.config/claude-code/agent-browser-config.env`
- ✅ Shell integration configured (Zsh/Bash)
- ✅ Environment variables set
- ✅ Ready for Claude Code use
- ✅ Documentation generated
- ✅ Setup script available: `~/.local/bin/setup-agent-browser.sh`

---

## 🚀 Next Steps

1. **Close and reopen your terminal** to load the new configuration

2. **Verify installation:**
   ```bash
   agent-browser --help
   ```

3. **Try a quick test:**
   ```bash
   agent-browser open https://example.com
   agent-browser screenshot
   ```

4. **Use in Claude Code** for automated testing and UI automation

5. **Explore advanced features** as needed

---

## 📞 Support & Help

If you encounter issues:

1. Check configuration: `cat ~/.config/claude-code/agent-browser-config.env`
2. Verify installation: `which agent-browser`
3. Run help: `agent-browser --help`
4. Check GitHub: https://github.com/vercel-labs/agent-browser

---

## Summary

Agent Browser is now **globally installed and configured** for seamless use across all terminals and in Claude Code. You can use it immediately for:

- Web testing and automation
- UI interaction testing
- Accessibility testing
- Visual regression testing
- E2E testing
- Web scraping

**Enjoy automated browser testing in Claude Code! 🎉**

---

**Installation Date:** 2026-01-16
**Configuration Version:** 1.0
**Status:** ✅ **COMPLETE AND READY TO USE**
