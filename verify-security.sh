#!/bin/bash
# verify-security.sh
# Script to run local security and vulnerability scans on Unix/macOS/Linux

set -e

echo -e "\033[36mRunning local security checks for nvx...\033[0m"

# 1. Check for govulncheck
if ! command -v govulncheck &> /dev/null; then
    if [ -f "$HOME/go/bin/govulncheck" ]; then
        GOVULNCHECK="$HOME/go/bin/govulncheck"
    else
        echo -e "\033[33mInstalling govulncheck...\033[0m"
        go install golang.org/x/vuln/cmd/govulncheck@v1.0.1
        GOVULNCHECK="$HOME/go/bin/govulncheck"
    fi
else
    GOVULNCHECK="govulncheck"
fi

echo -e "\n\033[36m1. Running govulncheck...\033[0m"
$GOVULNCHECK ./...

# 2. Check for gosec
if ! command -v gosec &> /dev/null; then
    if [ -f "$HOME/go/bin/gosec" ]; then
        GOSEC="$HOME/go/bin/gosec"
    else
        echo -e "\033[33mInstalling gosec...\033[0m"
        go install github.com/securego/gosec/v2/cmd/gosec@v2.16.0
        GOSEC="$HOME/go/bin/gosec"
    fi
else
    GOSEC="gosec"
fi

echo -e "\n\033[36m2. Running gosec...\033[0m"
$GOSEC -exclude=G204,G304,G301,G306 ./...

echo -e "\n\033[32mAll security scans completed successfully!\033[0m"
