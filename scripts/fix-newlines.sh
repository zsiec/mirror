#!/bin/bash

# fix-newlines.sh - Ensures all files in the repository end with a newline
# Usage: ./scripts/fix-newlines.sh [--dry-run] [--verbose]

set -uo pipefail

# Configuration
DRY_RUN=false
VERBOSE=false
FIXED_COUNT=0
CHECKED_COUNT=0

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [--dry-run] [--verbose]"
            echo ""
            echo "Ensures all files in the repository end with a newline."
            echo ""
            echo "Options:"
            echo "  --dry-run    Show what would be changed without making changes"
            echo "  --verbose    Show detailed output for all files checked"
            echo "  --help       Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option $1"
            exit 1
            ;;
    esac
done

# Function to check if file ends with newline
needs_newline() {
    local file="$1"
    
    # Skip empty files
    if [[ ! -s "$file" ]]; then
        return 1
    fi
    
    # Check if last character is newline
    if [[ $(tail -c1 "$file" | wc -l) -eq 0 ]]; then
        return 0  # Needs newline
    else
        return 1  # Already has newline
    fi
}

# Function to add newline to file
add_newline() {
    local file="$1"
    echo >> "$file"
}

# Function to log verbose output
log_verbose() {
    if [[ "$VERBOSE" == true ]]; then
        echo -e "$1"
    fi
}

# Function to check if file is text
is_text_file() {
    local file="$1"
    
    # Check file extension first for common text files
    case "$file" in
        *.txt|*.md|*.go|*.yaml|*.yml|*.json|*.toml|*.sh|*.py|*.js|*.ts|*.html|*.css|*.xml|*.sql|*.conf|*.cfg|*.ini|*.env|*.log|Makefile|Dockerfile|*.gitignore|*.gitattributes|LICENSE|README|CHANGELOG|CONTRIBUTING)
            return 0
            ;;
    esac
    
    # Use file command as fallback, but don't fail on error
    local file_output
    if file_output=$(file "$file" 2>/dev/null); then
        if echo "$file_output" | grep -q "text"; then
            return 0
        fi
    fi
    
    return 1
}

# Main processing function
process_file() {
    local file="$1"
    ((CHECKED_COUNT++))
    
    if needs_newline "$file"; then
        if [[ "$DRY_RUN" == true ]]; then
            echo -e "${YELLOW}[DRY-RUN]${NC} Would add newline to: $file"
        else
            add_newline "$file"
            echo -e "${GREEN}✓${NC} Fixed newline in: $file"
        fi
        ((FIXED_COUNT++))
    else
        log_verbose "${BLUE}✓${NC} Already has newline: $file"
    fi
}

echo -e "${BLUE}🔍 Checking files for trailing newlines...${NC}"
if [[ "$DRY_RUN" == true ]]; then
    echo -e "${YELLOW}📋 DRY RUN MODE - No changes will be made${NC}"
fi
echo ""

# Get repository root
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || echo ".")
cd "$REPO_ROOT"

# Create temporary file list to avoid pipe subshell issues
TEMP_FILE_LIST=$(mktemp)
trap "rm -f $TEMP_FILE_LIST" EXIT

# Get all files (tracked and untracked, but exclude .git directory and common ignore patterns)
{
    git ls-files  # Get all tracked files
    git ls-files --others --exclude-standard  # Get untracked files (excluding .gitignore patterns)
} | sort -u > "$TEMP_FILE_LIST"

# Process each file
while IFS= read -r file; do
    # Skip if file doesn't exist (could be deleted)
    if [[ ! -f "$file" ]]; then
        continue
    fi
    
    # Skip binary files
    if ! is_text_file "$file"; then
        log_verbose "${BLUE}⏭${NC}  Skipping non-text file: $file"
        continue
    fi
    
    process_file "$file"
    
done < "$TEMP_FILE_LIST"

# Summary
echo ""
echo -e "${BLUE}📊 Summary:${NC}"
echo -e "   Files checked: ${CHECKED_COUNT}"
if [[ "$DRY_RUN" == true ]]; then
    echo -e "   Files that would be fixed: ${FIXED_COUNT}"
else
    echo -e "   Files fixed: ${FIXED_COUNT}"
fi

if [[ "$FIXED_COUNT" -eq 0 ]]; then
    echo -e "${GREEN}🎉 All files already have proper trailing newlines!${NC}"
else
    if [[ "$DRY_RUN" == true ]]; then
        echo -e "${YELLOW}💡 Run without --dry-run to apply fixes${NC}"
    else
        echo -e "${GREEN}✅ Fixed trailing newlines in ${FIXED_COUNT} files${NC}"
    fi
fi
