#!/bin/bash

# Alpacon CLI Test Script
# This script tests various alpacon CLI functionalities

set -e  # Exit on any error

# Configuration
SERVER_NAME="SERVER_NAME" # amazon-linux-1
LOCAL_PATH="/your/local/path"
REMOTE_ROOT_PATH="/root"
REMOTE_USER_PATH="/your/remote/path"
TEST_FILE="test.txt"
WORKSPACE_URL="WORKSPACE_URL" # https://dev.alpacon.io/alpacax
TEST_CONTENT="Hello from Alpacon CLI test! $(date)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

run_test() {
    local test_name="$1"
    local command="$2"

    echo
    log_info "Running test: $test_name"
    echo "Command: $command"
    echo "----------------------------------------"

    # eval 대신 직접 실행하여 syntax error 방지
    if bash -c "$command"; then
        log_success "Test passed: $test_name"
    else
        log_error "Test failed: $test_name"
        return 1
    fi
}

cleanup() {
    log_info "Cleaning up test files..."

    # Remove downloaded test files only (keep original test.txt)
    rm -f "$LOCAL_PATH/downloaded_user_$TEST_FILE" "$LOCAL_PATH/downloaded_root_$TEST_FILE" 2>/dev/null || true

    # Remove downloaded test folders
    rm -rf "$LOCAL_PATH/downloaded_user_$TEST_FOLDER" "$LOCAL_PATH/downloaded_root_$TEST_FOLDER" 2>/dev/null || true

    # Remove remote test files and folders
    log_info "Cleaning up remote test files..."
    alpacon exec $SERVER_NAME "rm -f $REMOTE_ROOT_PATH/$TEST_FILE $REMOTE_USER_PATH/$TEST_FILE" 2>/dev/null || true
    alpacon exec -u root $SERVER_NAME "rm -f $REMOTE_ROOT_PATH/$TEST_FILE" 2>/dev/null || true

    # Clean up remote test folders
    alpacon exec $SERVER_NAME "rm -rf $REMOTE_USER_PATH/$TEST_FOLDER" 2>/dev/null || true
    alpacon exec -u root $SERVER_NAME "rm -rf $REMOTE_ROOT_PATH/$TEST_FOLDER" 2>/dev/null || true
}

# Trap to cleanup on exit
trap cleanup EXIT

echo "=========================================="
echo "       Alpacon CLI Test Suite"
echo "=========================================="
echo "Local Path: $LOCAL_PATH"
echo "Server: $SERVER_NAME"
echo "Remote Paths: $REMOTE_ROOT_PATH, $REMOTE_USER_PATH"
echo "Test File: $TEST_FILE"
echo "=========================================="

# Check if alpacon is available
if ! command -v alpacon &> /dev/null; then
    log_error "alpacon command not found. Please ensure it's installed and in PATH."
    exit 1
fi

# Check login status and login if necessary
log_info "Checking Alpacon login status..."
if ! alpacon server ls &>/dev/null; then
    log_warning "Not logged in to Alpacon. Attempting to login..."

    if [ "$WORKSPACE_URL" = "https://your-workspace.alpacon.io" ]; then
        log_error "Please update WORKSPACE_URL in the configuration section with your actual workspace URL."
        echo "Edit the script and change WORKSPACE_URL=\"https://your-workspace.alpacon.io\" to your actual workspace URL."
        exit 1
    fi

    log_info "Logging in to workspace: $WORKSPACE_URL"
    echo "Please complete the login process in your browser..."

    if alpacon login "$WORKSPACE_URL"; then
        log_success "Successfully logged in to Alpacon!"
    else
        log_error "Failed to login to Alpacon. Please check your credentials and workspace URL."
        echo "You can manually login with: alpacon login $WORKSPACE_URL"
        exit 1
    fi
else
    log_success "Already logged in to Alpacon!"
fi

# Create test file locally
log_info "Creating local test file..."
echo "$TEST_CONTENT" > "$LOCAL_PATH/$TEST_FILE"
log_success "Created test file: $LOCAL_PATH/$TEST_FILE"

# Create test folder and files for folder upload/download tests
log_info "Creating test folder and files..."
TEST_FOLDER="test_folder"
mkdir -p "$LOCAL_PATH/$TEST_FOLDER"
echo "Content of test1.txt in folder $(date)" > "$LOCAL_PATH/$TEST_FOLDER/test1.txt"
echo "Content of test2.txt in folder $(date)" > "$LOCAL_PATH/$TEST_FOLDER/test2.txt"
echo "Nested folder content $(date)" > "$LOCAL_PATH/$TEST_FOLDER/nested_file.txt"
log_success "Created test folder: $LOCAL_PATH/$TEST_FOLDER with test files"

echo
echo "=========================================="
echo "         1. BASIC CONNECTIVITY TESTS"
echo "=========================================="

# Test 1: Basic server connectivity
run_test "Server connectivity" \
    "alpacon exec $SERVER_NAME 'echo \"Connection successful\"'"

# Test 2: Check server information
run_test "Server information" \
    "alpacon exec $SERVER_NAME 'uname -a'"

# Test 3: List servers
run_test "List servers" \
    "alpacon server ls"

echo
echo "=========================================="
echo "         2. COMMAND EXECUTION TESTS"
echo "=========================================="

# Test 4: Basic command execution
run_test "Basic command execution" \
    "alpacon exec $SERVER_NAME 'pwd && whoami'"

# Test 5: Root user command execution
run_test "Root user command execution" \
    "alpacon exec -u root $SERVER_NAME 'whoami && id'"

# Test 6: SSH-style user specification
run_test "SSH-style root execution" \
    "alpacon exec root@$SERVER_NAME 'whoami'"

# Test 7: Directory listing
run_test "Directory listing" \
    "alpacon exec $SERVER_NAME 'ls -la /home'"

# Test 8: Environment check
run_test "Environment check" \
    "alpacon exec $SERVER_NAME 'env | grep -E \"(USER|HOME|PATH)\" | head -5'"

echo
echo "=========================================="
echo "         3. FILE TRANSFER TESTS (UPLOAD)"
echo "=========================================="

# Test 9: Upload to user home directory
run_test "Upload to user home directory" \
    "alpacon cp '$LOCAL_PATH/$TEST_FILE' '$SERVER_NAME:$REMOTE_USER_PATH/'"

# Test 10: Verify uploaded file
run_test "Verify uploaded file in user home" \
    "alpacon exec $SERVER_NAME 'cat $REMOTE_USER_PATH/$TEST_FILE'"

# Test 11: Upload to root directory (as root)
run_test "Upload to root directory as root" \
    "alpacon cp -u root '$LOCAL_PATH/$TEST_FILE' '$SERVER_NAME:$REMOTE_ROOT_PATH/'"

# Test 12: Verify root upload
run_test "Verify uploaded file in root directory" \
    "alpacon exec -u root $SERVER_NAME 'cat $REMOTE_ROOT_PATH/$TEST_FILE'"

echo
echo "=========================================="
echo "         4. FILE TRANSFER TESTS (DOWNLOAD)"
echo "=========================================="

# Test 13: Download from user directory
run_test "Download from user directory" \
    "alpacon cp '$SERVER_NAME:$REMOTE_USER_PATH/$TEST_FILE' '$LOCAL_PATH/'"

# Test 14: Verify downloaded file (원본 파일이 다운로드됨)
run_test "Verify downloaded file content" \
    "test -f '$LOCAL_PATH/$TEST_FILE' && cat '$LOCAL_PATH/$TEST_FILE' | grep -q 'Hello from Alpacon CLI test'"

# Clean up downloaded file to prepare for root download test (원본 파일은 보존, 복사본 생성)
cp "$LOCAL_PATH/$TEST_FILE" "$LOCAL_PATH/downloaded_user_$TEST_FILE" 2>/dev/null || true

# Test 15: Download from root directory (as root)
run_test "Download from root directory as root" \
    "alpacon cp -u root '$SERVER_NAME:$REMOTE_ROOT_PATH/$TEST_FILE' '$LOCAL_PATH/'"

# Test 16: Verify root downloaded file
run_test "Verify root downloaded file content" \
    "test -f '$LOCAL_PATH/$TEST_FILE' && cat '$LOCAL_PATH/$TEST_FILE' | grep -q 'Hello from Alpacon CLI test'"

# Rename root downloaded file to avoid conflicts (원본 파일은 보존, 복사본 생성)
cp "$LOCAL_PATH/$TEST_FILE" "$LOCAL_PATH/downloaded_root_$TEST_FILE" 2>/dev/null || true

echo
echo "=========================================="
echo "         5. FOLDER TRANSFER TESTS (RECURSIVE)"
echo "=========================================="

# Test 17: Upload folder to user directory
run_test "Upload folder to user directory" \
    "alpacon cp -r '$LOCAL_PATH/$TEST_FOLDER' '$SERVER_NAME:$REMOTE_USER_PATH/'"

# Test 18: Verify uploaded folder contents
run_test "Verify uploaded folder contents" \
    "alpacon exec $SERVER_NAME 'ls -la $REMOTE_USER_PATH/$TEST_FOLDER/ && cat $REMOTE_USER_PATH/$TEST_FOLDER/test1.txt'"

# Test 19: Upload folder to root directory (as root)
run_test "Upload folder to root directory as root" \
    "alpacon cp -r -u root '$LOCAL_PATH/$TEST_FOLDER' '$SERVER_NAME:$REMOTE_ROOT_PATH/'"

# Test 20: Verify root uploaded folder
run_test "Verify root uploaded folder contents" \
    "alpacon exec -u root $SERVER_NAME 'ls -la $REMOTE_ROOT_PATH/$TEST_FOLDER/ && cat $REMOTE_ROOT_PATH/$TEST_FOLDER/test2.txt'"

# Test 21: Download folder from user directory
run_test "Download folder from user directory" \
    "alpacon cp -r '$SERVER_NAME:$REMOTE_USER_PATH/$TEST_FOLDER' '$LOCAL_PATH/'"

# Test 22: Verify downloaded folder contents
run_test "Verify downloaded folder contents" \
    "test -d '$LOCAL_PATH/$TEST_FOLDER' && test -f '$LOCAL_PATH/$TEST_FOLDER/test1.txt' && cat '$LOCAL_PATH/$TEST_FOLDER/test1.txt' | grep -q 'Content of test1.txt in folder'"

# Rename downloaded folder to avoid conflicts
mv "$LOCAL_PATH/$TEST_FOLDER" "$LOCAL_PATH/downloaded_user_$TEST_FOLDER" 2>/dev/null || true

# Test 23: Download folder from root directory (as root)
run_test "Download folder from root directory as root" \
    "alpacon cp -r -u root '$SERVER_NAME:$REMOTE_ROOT_PATH/$TEST_FOLDER' '$LOCAL_PATH/'"

# Test 24: Verify root downloaded folder
run_test "Verify root downloaded folder contents" \
    "test -d '$LOCAL_PATH/$TEST_FOLDER' && test -f '$LOCAL_PATH/$TEST_FOLDER/nested_file.txt' && cat '$LOCAL_PATH/$TEST_FOLDER/nested_file.txt' | grep -q 'Nested folder content'"

# Rename root downloaded folder to avoid conflicts
mv "$LOCAL_PATH/$TEST_FOLDER" "$LOCAL_PATH/downloaded_root_$TEST_FOLDER" 2>/dev/null || true

echo
echo "=========================================="
echo "         6. WEBSH TESTS"
echo "=========================================="

# Test 25: Websh command execution
run_test "Websh command execution" \
    "alpacon websh $SERVER_NAME 'echo \"Websh test successful\" && date'"

# Test 26: Websh as root user
run_test "Websh as root user" \
    "alpacon websh -u root $SERVER_NAME 'whoami && pwd'"

# Test 27: Websh with SSH-style syntax
run_test "Websh with SSH-style syntax" \
    "alpacon websh root@$SERVER_NAME 'id'"

echo
echo "=========================================="
echo "         7. ADVANCED TESTS"
echo "=========================================="

# Test 28: Multiple file operations
log_info "Creating additional test files..."
echo "File 1 content" > "$LOCAL_PATH/test1.txt"
echo "File 2 content" > "$LOCAL_PATH/test2.txt"

run_test "Multiple file upload" \
    "alpacon cp '$LOCAL_PATH/test1.txt' '$LOCAL_PATH/test2.txt' '$SERVER_NAME:$REMOTE_USER_PATH/'"

# Test 29: Verify multiple files
run_test "Verify multiple uploaded files" \
    "alpacon exec $SERVER_NAME 'ls -la $REMOTE_USER_PATH/test*.txt'"

# Test 30: Directory creation and file operations
run_test "Create directory and upload" \
    "alpacon exec $SERVER_NAME 'mkdir -p $REMOTE_USER_PATH/test_dir' && alpacon cp '$LOCAL_PATH/$TEST_FILE' '$SERVER_NAME:$REMOTE_USER_PATH/test_dir/'"

# Test 31: Complex command with pipes
run_test "Complex command with pipes" \
    "alpacon exec $SERVER_NAME 'ps aux | grep -v grep | head -5'"

# Test 32: System information gathering
run_test "System information gathering" \
    "alpacon exec $SERVER_NAME 'df -h | head -5 && free -h'"

#echo
#echo "=========================================="
#echo "         7. ERROR HANDLING TESTS"
#echo "=========================================="
#
## Test 25: Non-existent file download (should fail gracefully)
#log_info "Testing error handling for non-existent file..."
#if alpacon cp "$SERVER_NAME:/non/existent/file.txt" "$LOCAL_PATH/" 2>/dev/null; then
#    log_error "Expected failure for non-existent file, but command succeeded"
#else
#    log_success "Correctly handled non-existent file error"
#fi
#
## Test 26: Permission denied test (should provide helpful error)
#log_info "Testing permission denied scenario..."
#if alpacon exec $SERVER_NAME 'cat /etc/shadow' 2>/dev/null; then
#    log_warning "Unexpected success reading /etc/shadow (server might have unusual permissions)"
#else
#    log_success "Correctly handled permission denied error"
#fi

echo
echo "=========================================="
echo "         8. CLEANUP AND SUMMARY"
echo "=========================================="

# Clean up additional test files
rm -f "$LOCAL_PATH/$TEST_FILE
rm -f "$LOCAL_PATH/test1.txt" "$LOCAL_PATH/test2.txt"
rm -f "$LOCAL_PATH/downloaded_user_$TEST_FILE" "$LOCAL_PATH/downloaded_root_$TEST_FILE"

# Clean up test folders
rm -rf "$LOCAL_PATH/downloaded_user_$TEST_FOLDER" "$LOCAL_PATH/downloaded_root_$TEST_FOLDER" 2>/dev/null || true

# Remote cleanup
alpacon exec $SERVER_NAME "rm -f $REMOTE_USER_PATH/test*.txt" 2>/dev/null || true
alpacon exec $SERVER_NAME "rm -rf $REMOTE_USER_PATH/test_dir" 2>/dev/null || true
alpacon exec $SERVER_NAME "rm -rf $REMOTE_USER_PATH/$TEST_FOLDER" 2>/dev/null || true
alpacon exec -u root $SERVER_NAME "rm -rf $REMOTE_ROOT_PATH/$TEST_FOLDER" 2>/dev/null || true

log_success "Test suite completed!"
echo
echo "=========================================="
echo "              MANUAL TESTS"
echo "=========================================="
echo "The following tests require manual interaction:"
echo
echo "1. Interactive websh session:"
echo "   alpacon websh $SERVER_NAME"
echo
echo "2. Interactive websh as root:"
echo "   alpacon websh -u root $SERVER_NAME"
echo
echo "3. Shared websh session:"
echo "   alpacon websh $SERVER_NAME --share"
echo
echo "4. MFA authentication (if configured):"
echo "   (Try any command and follow MFA prompts)"
echo "=========================================="

log_info "All automated tests completed. Check the output above for any failures."
echo "To run manual tests, execute the commands listed in the MANUAL TESTS section."
