# 🌐 Agent Browser - Quick Reference Guide

**Last Updated:** 2026-01-16
**Status:** ✅ **GLOBALLY INSTALLED & CONFIGURED**

---

## 🚀 Quick Start (30 seconds)

```bash
# 1. Open your terminal
# 2. Verify installation
which agent-browser

# 3. Try a command
agent-browser open https://example.com
agent-browser screenshot
agent-browser close
```

---

## 📍 Location Reference

| Item | Path |
|------|------|
| **Executable** | `/opt/homebrew/bin/agent-browser` |
| **Config** | `~/.config/claude-code/agent-browser-config.env` |
| **Setup Script** | `~/.local/bin/setup-agent-browser.sh` |
| **Demo Script** | `~/.local/bin/agent-browser-demo.sh` |
| **This Guide** | `AGENT_BROWSER_SETUP.md` |

---

## ⚡ Common Commands

### Navigation
```bash
agent-browser open https://example.com      # Open URL
agent-browser back                          # Go back
agent-browser forward                       # Go forward
agent-browser reload                        # Reload page
agent-browser close                         # Close browser
```

### Interaction
```bash
agent-browser click ".button"               # Click element
agent-browser type "#input" "text"          # Type text
agent-browser fill "#input" "text"          # Clear and type
agent-browser hover ".element"              # Hover element
agent-browser scroll down 300               # Scroll down
```

### Information
```bash
agent-browser screenshot test.png           # Take screenshot
agent-browser get text                      # Get all text
agent-browser get url                       # Get current URL
agent-browser get html                      # Get page HTML
agent-browser snapshot                      # Get accessibility tree
```

### Settings
```bash
agent-browser set viewport 1920 1080        # Set viewport
agent-browser set device "iPhone 12"        # Set device
agent-browser set media dark                # Dark mode
agent-browser set offline on                # Go offline
```

---

## 🎯 Real-World Examples

### Example 1: Test Web Page Load
```bash
#!/bin/bash
agent-browser open http://localhost:3000
agent-browser wait "h1"
agent-browser screenshot loaded.png
echo "✅ Page loaded successfully"
```

### Example 2: Form Submission
```bash
#!/bin/bash
agent-browser open http://localhost:3000/form
agent-browser type "[name='email']" "user@example.com"
agent-browser type "[name='password']" "password123"
agent-browser click "[type='submit']"
agent-browser wait ".success"
agent-browser screenshot success.png
```

### Example 3: Mobile Testing
```bash
#!/bin/bash
agent-browser open http://localhost:3000
agent-browser set viewport 375 812  # iPhone size
agent-browser screenshot mobile.png
echo "✅ Mobile screenshot captured"
```

### Example 4: Accessibility Check
```bash
#!/bin/bash
agent-browser open http://localhost:3000
agent-browser snapshot > accessibility.json
echo "✅ Accessibility tree saved"
```

---

## 📊 In Claude Code Usage

### Method 1: Direct CLI
```bash
# Use agent-browser directly in Claude Code tasks
agent-browser open http://localhost:5000/dashboard
agent-browser screenshot page.png
agent-browser get text
```

### Method 2: Shell Script
```bash
#!/bin/bash
# Test Lending page
agent-browser open http://localhost:5000/dashboard/lending
agent-browser wait "table"
agent-browser screenshot lending.png
agent-browser get count "tbody tr"
```

### Method 3: JavaScript/Node.js
```javascript
const { execSync } = require('child_process');

// Open page
execSync('agent-browser open http://localhost:5000');

// Take screenshot
execSync('agent-browser screenshot page.png');

// Get content
const text = execSync('agent-browser get text').toString();
console.log(text);
```

### Method 4: Python
```python
import subprocess

# Use agent-browser from Python
subprocess.run(['agent-browser', 'open', 'http://localhost:5000'])
subprocess.run(['agent-browser', 'screenshot', 'page.png'])

# Get output
result = subprocess.run(['agent-browser', 'get', 'url'],
                       capture_output=True, text=True)
print(result.stdout)
```

---

## 🔧 Troubleshooting Quick Fix

### Command not found?
```bash
# Reload shell
source ~/.zshrc  # or ~/.bash_profile

# Or restart terminal
```

### Environment variables not loading?
```bash
# Check config file
cat ~/.config/claude-code/agent-browser-config.env

# Manually source
source ~/.config/claude-code/agent-browser-config.env

# Verify
echo $AGENT_BROWSER_INSTALLED
```

### Browser won't open?
```bash
# Try without headless mode
AGENT_BROWSER_HEADLESS=false agent-browser open https://example.com
```

### Timeout errors?
```bash
# Increase timeout
export AGENT_BROWSER_TIMEOUT=60000

# Or wait explicitly
agent-browser wait 3000
```

---

## 💡 Pro Tips

### Tip 1: Chaining Commands
```bash
# Chain multiple commands
agent-browser open https://example.com && \
  agent-browser wait 2000 && \
  agent-browser screenshot page.png && \
  echo "Done!"
```

### Tip 2: Error Handling
```bash
#!/bin/bash
# Safe script with error handling
set -e  # Exit on error

agent-browser open https://example.com
agent-browser wait "h1" || { echo "Page load failed"; exit 1; }
agent-browser screenshot page.png
echo "✅ Success"
```

### Tip 3: Loop & Test Multiple Sites
```bash
#!/bin/bash
sites=("https://example.com" "https://google.com" "https://github.com")

for site in "${sites[@]}"; do
  agent-browser open "$site"
  agent-browser screenshot "${site##*/}.png"
  echo "✅ Tested: $site"
done
```

### Tip 4: Environment Variables for Different Scenarios
```bash
# Production testing (with timeout)
export AGENT_BROWSER_TIMEOUT=60000
export AGENT_BROWSER_HEADLESS=true

# Local development (visual feedback)
export AGENT_BROWSER_HEADLESS=false
export AGENT_BROWSER_TIMEOUT=10000
```

### Tip 5: Debugging
```bash
# Enable debug output
export AGENT_BROWSER_DEBUG=true

# Run command with debugging
agent-browser open https://example.com
```

---

## 📚 Help Commands

```bash
# General help
agent-browser --help
agent-browser help

# Help for specific command
agent-browser help open
agent-browser help click
agent-browser help set

# List all commands
agent-browser help | grep "^  "
```

---

## 🔄 Available in Any Context

### ✅ Works In:
- **New Terminals** - Any terminal window automatically loads config
- **Shell Scripts** - Bash, Zsh, Fish, etc.
- **Node.js** - Via `child_process.execSync()`
- **Python** - Via `subprocess` module
- **Ruby** - Via backticks or `system()` call
- **Claude Code** - Direct CLI usage or script execution
- **GitHub Actions** - Installed globally, ready to use
- **Docker** - Can be added to Dockerfiles
- **CI/CD Pipelines** - Available as CLI tool

---

## 📦 Package Information

```bash
# View agent-browser information
npm list -g agent-browser

# Expected output:
# /opt/homebrew/lib
# └── agent-browser@0.5.0

# Check npm registry
npm info agent-browser
```

---

## 🎓 Learning Resources

| Resource | Link |
|----------|------|
| **GitHub** | https://github.com/vercel-labs/agent-browser |
| **Vercel Labs** | https://vercel.com/labs |
| **Browser Automation** | https://playwright.dev |
| **Puppeteer** | https://pptr.dev |

---

## ⚙️ Advanced Configuration

### Custom Environment Variables

Create a `.env` file in your project:
```bash
# .env
AGENT_BROWSER_HEADLESS=false
AGENT_BROWSER_TIMEOUT=30000
AGENT_BROWSER_DOWNLOADS=/tmp/downloads
```

Load in script:
```bash
#!/bin/bash
if [ -f .env ]; then
  export $(cat .env | grep AGENT_BROWSER | xargs)
fi

agent-browser open https://example.com
```

### Custom Device Profiles

```bash
# Define custom viewport
CUSTOM_VIEWPORT="1366x768"

# Use in script
agent-browser set viewport ${CUSTOM_VIEWPORT%x*} ${CUSTOM_VIEWPORT#*x}
agent-browser open https://example.com
agent-browser screenshot custom-viewport.png
```

---

## 🚨 Common Pitfalls & Solutions

| Issue | Solution |
|-------|----------|
| Command not found | `source ~/.zshrc` |
| Port already in use | Kill process or use different port |
| Screenshot won't save | Use absolute path: `/tmp/screenshot.png` |
| Timeout on slow sites | `export AGENT_BROWSER_TIMEOUT=60000` |
| Headless mode issues | `AGENT_BROWSER_HEADLESS=false` |
| Memory usage high | Close browser: `agent-browser close` |

---

## ✨ Version Information

```
Tool:              Agent Browser
Version:           0.5.0
Install Date:      2026-01-16
Install Method:    Global (npm/Homebrew)
Executable:        /opt/homebrew/bin/agent-browser
Config:            ~/.config/claude-code/agent-browser-config.env
Status:            ✅ Ready
```

---

## 🎉 You're All Set!

Agent Browser is now:
- ✅ **Installed globally** - Available everywhere
- ✅ **Configured** - Automatic environment setup
- ✅ **Shell integrated** - Loads on new terminal sessions
- ✅ **Claude Code ready** - Use directly in tasks
- ✅ **Documented** - Complete guides available

### Get Started Now:
```bash
agent-browser open https://example.com
agent-browser screenshot
echo "🎉 Agent Browser is working!"
```

---

**For complete documentation, see:** `AGENT_BROWSER_SETUP.md`
