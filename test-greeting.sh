#!/bin/bash
# Manual test of the greeting logic

echo "=== Testing Home Manager Greeting Logic ==="
echo "User: $(whoami)"
echo "Home: $HOME"
echo "Shell: $SHELL"
echo ""

# Check if config exists
if [ -f ~/.config/home-manager/home.nix ]; then
  echo "✅ Home Manager config exists"
  CONFIG_EXISTS=1
else
  echo "❌ Home Manager config missing"
  CONFIG_EXISTS=0
fi

# Check if Home Manager is activated
if home-manager generations &>/dev/null && [[ "$(home-manager generations 2>/dev/null | wc -l)" -gt 0 ]]; then
  echo "✅ Home Manager is activated ($(home-manager generations | wc -l) generations)"
  HM_ACTIVATED=1
else
  echo "❌ Home Manager not activated yet"
  HM_ACTIVATED=0
fi

echo ""
if [[ $CONFIG_EXISTS -eq 1 && $HM_ACTIVATED -eq 0 ]]; then
  echo "Should show greeting: YES"
  echo "Conditions met: Config exists AND Home Manager not activated"
else
  echo "Should show greeting: NO"
  if [[ $CONFIG_EXISTS -eq 0 ]]; then
    echo "Reason: Home Manager config doesn't exist yet"
  elif [[ $HM_ACTIVATED -eq 1 ]]; then
    echo "Reason: Home Manager already activated"
  fi
fi

# If conditions are met, show the greeting
if [[ $CONFIG_EXISTS -eq 1 && $HM_ACTIVATED -eq 0 ]]; then
  echo ""
  echo "=== GREETING PREVIEW ==="
  echo ""
  echo "╭─────────────────────────────────────────────────────────────╮"
  echo "│                    🏠 Home Manager Ready!                   │"
  echo "╰─────────────────────────────────────────────────────────────╯"
  echo ""
  echo "Welcome! Your personal Home Manager configuration is ready."
  echo ""
  echo "🚀 To activate your home environment:"
  echo "   cd ~/.config/home-manager"
  echo "   home-manager switch --flake .#$(whoami)"
  echo ""
  echo "📝 After activation, customize your setup:"
  echo "   \$EDITOR ~/.config/home-manager/home.nix"
  echo ""
  echo "📚 Documentation and examples:"
  echo "   cat /etc/home-manager-templates/README.md"
  echo ""
  echo "Happy configuring! 🎉"
  echo ""
  echo "=== END GREETING PREVIEW ==="
else
  echo ""
  echo "ℹ️  No greeting shown - either config missing or already activated"
fi
