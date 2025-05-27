#!/bin/bash

# Mirror Frame Visualization Demo Script
# Streams H.264 video via SRT to demonstrate frame analysis capabilities

set -e

# Configuration
SRT_HOST="127.0.0.1"
SRT_PORT="30000"
STREAM_ID="demo-stream-001"
VIDEO_RESOLUTION="1920x1080"
VIDEO_FRAMERATE="30"
VIDEO_BITRATE="4000k"
AUDIO_BITRATE="128k"
GOP_SIZE="30"  # GOP every 1 second at 30fps

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_header() {
    echo -e "${PURPLE}================================${NC}"
    echo -e "${PURPLE}$1${NC}"
    echo -e "${PURPLE}================================${NC}"
}

# Function to check if FFmpeg is installed and has SRT support
check_ffmpeg() {
    print_status "Checking FFmpeg installation..."
    
    if ! command -v ffmpeg &> /dev/null; then
        print_error "FFmpeg is not installed or not in PATH"
        echo "Please install FFmpeg with SRT support:"
        echo "  macOS: brew install ffmpeg"
        echo "  Ubuntu: sudo apt install ffmpeg"
        echo "  Or compile from source with --enable-libsrt"
        exit 1
    fi
    
    # Check if FFmpeg has SRT support
    if ! ffmpeg -protocols 2>&1 | grep -q "srt"; then
        print_error "FFmpeg does not have SRT support"
        echo "Please install FFmpeg with SRT support:"
        echo "  macOS: brew install ffmpeg"
        echo "  Or compile from source with --enable-libsrt"
        exit 1
    fi
    
    print_success "FFmpeg with SRT support found"
}

# Function to check if Mirror server is running
check_mirror_server() {
    print_status "Checking if Mirror server is running..."
    
    if ! curl -k -s "https://localhost:8081/health" > /dev/null 2>&1; then
        print_error "Mirror server is not running on https://localhost:8081"
        echo "Please start the Mirror server first:"
        echo "  cd /path/to/mirror && go run cmd/mirror/main.go"
        exit 1
    fi
    
    print_success "Mirror server is running"
}

# Function to generate test video content
generate_test_content() {
    local content_type="$1"
    local output_args=""
    
    case "$content_type" in
        "testsrc")
            print_status "Generating colorful test pattern with frame counters..."
            output_args="-f lavfi -i testsrc2=size=${VIDEO_RESOLUTION}:rate=${VIDEO_FRAMERATE}"
            ;;
        "mandelbrot")
            print_status "Generating animated Mandelbrot set..."
            output_args="-f lavfi -i mandelbrot=size=${VIDEO_RESOLUTION}:rate=${VIDEO_FRAMERATE}"
            ;;
        "noise")
            print_status "Generating noise pattern..."
            output_args="-f lavfi -i rgbtestsrc=size=${VIDEO_RESOLUTION}:rate=${VIDEO_FRAMERATE}"
            ;;
        "life")
            print_status "Generating Conway's Game of Life..."
            output_args="-f lavfi -i life=size=${VIDEO_RESOLUTION}:rate=${VIDEO_FRAMERATE}"
            ;;
        *)
            print_error "Unknown content type: $content_type"
            exit 1
            ;;
    esac
    
    echo "$output_args"
}

# Function to create audio test signal
generate_audio() {
    print_status "Generating sine wave audio test signal..."
    echo "-f lavfi -i sine=frequency=440:duration=3600"
}

# Function to stream video via SRT
stream_srt() {
    local content_type="${1:-testsrc}"
    local profile="${2:-baseline}"
    
    print_header "Starting SRT Stream"
    print_status "Content: $content_type"
    print_status "Profile: $profile"
    print_status "Resolution: $VIDEO_RESOLUTION"
    print_status "Framerate: ${VIDEO_FRAMERATE}fps"
    print_status "Bitrate: $VIDEO_BITRATE"
    print_status "GOP Size: $GOP_SIZE frames"
    print_status "Target: srt://${SRT_HOST}:${SRT_PORT}?streamid=${STREAM_ID}"
    echo
    
    # Generate input arguments
    local video_input=$(generate_test_content "$content_type")
    local audio_input=$(generate_audio)
    
    # Set H.264 profile-specific settings
    local h264_profile=""
    local h264_level="4.0"
    case "$profile" in
        "baseline")
            h264_profile="-profile:v baseline -level:v 3.1"
            ;;
        "main")
            h264_profile="-profile:v main -level:v 4.0"
            ;;
        "high")
            h264_profile="-profile:v high -level:v 4.0"
            ;;
        *)
            print_warning "Unknown profile '$profile', using baseline"
            h264_profile="-profile:v baseline -level:v 3.1"
            ;;
    esac
    
    print_status "Starting FFmpeg stream..."
    print_warning "Press Ctrl+C to stop streaming"
    echo
    
    # FFmpeg command with detailed frame info
    ffmpeg \
        $video_input \
        $audio_input \
        -c:v libx264 \
        $h264_profile \
        -b:v $VIDEO_BITRATE \
        -maxrate $VIDEO_BITRATE \
        -bufsize $(echo $VIDEO_BITRATE | sed 's/k/*2k/' | bc) \
        -g $GOP_SIZE \
        -keyint_min $GOP_SIZE \
        -sc_threshold 0 \
        -force_key_frames "expr:gte(t,n_forced*1)" \
        -x264-params "nal-hrd=cbr:bframes=2:b-adapt=1:ref=3:weightp=1" \
        -c:a aac \
        -b:a $AUDIO_BITRATE \
        -ar 48000 \
        -ac 2 \
        -f mpegts \
        -mpegts_flags +initial_discontinuity \
        -streamid 0:256 \
        -streamid 1:257 \
        -metadata service_name="Mirror Demo Stream" \
        -metadata service_provider="Mirror Streaming Platform" \
        -y \
        "srt://${SRT_HOST}:${SRT_PORT}?streamid=${STREAM_ID}&mode=caller&transtype=live&latency=200" \
        2>&1 | while IFS= read -r line; do
            # Color-code FFmpeg output
            if [[ $line == *"error"* ]] || [[ $line == *"Error"* ]]; then
                echo -e "${RED}$line${NC}"
            elif [[ $line == *"warning"* ]] || [[ $line == *"Warning"* ]]; then
                echo -e "${YELLOW}$line${NC}"
            elif [[ $line == *"frame="* ]]; then
                echo -e "${GREEN}$line${NC}"
            elif [[ $line == *"Input"* ]] || [[ $line == *"Output"* ]]; then
                echo -e "${CYAN}$line${NC}"
            else
                echo "$line"
            fi
        done
}

# Function to show available content types
show_content_types() {
    print_header "Available Content Types"
    echo "testsrc    - Colorful test pattern with frame counters (default)"
    echo "mandelbrot - Animated Mandelbrot set fractal"
    echo "noise      - RGB noise pattern"
    echo "life       - Conway's Game of Life simulation"
    echo
}

# Function to show available H.264 profiles
show_profiles() {
    print_header "Available H.264 Profiles"
    echo "baseline - Baseline profile (maximum compatibility)"
    echo "main     - Main profile (better compression)"
    echo "high     - High profile (best compression, default)"
    echo
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [COMMAND] [OPTIONS]"
    echo
    echo "Commands:"
    echo "  stream [CONTENT] [PROFILE]  - Start streaming (default)"
    echo "  check                       - Check dependencies"
    echo "  content                     - Show available content types"
    echo "  profiles                    - Show available H.264 profiles"
    echo "  help                        - Show this help"
    echo
    echo "Examples:"
    echo "  $0                          - Stream test pattern with baseline profile"
    echo "  $0 stream mandelbrot high   - Stream Mandelbrot with high profile"
    echo "  $0 stream testsrc main      - Stream test pattern with main profile"
    echo "  $0 check                    - Check if all dependencies are available"
    echo
    echo "Configuration:"
    echo "  SRT Target: srt://${SRT_HOST}:${SRT_PORT}?streamid=${STREAM_ID}"
    echo "  Resolution: ${VIDEO_RESOLUTION}"
    echo "  Framerate: ${VIDEO_FRAMERATE}fps"
    echo "  Video Bitrate: ${VIDEO_BITRATE}"
    echo "  Audio Bitrate: ${AUDIO_BITRATE}"
    echo "  GOP Size: ${GOP_SIZE} frames"
    echo
    print_warning "Make sure the Mirror server is running before streaming!"
    echo "  Access the visualization UI at: https://localhost:8081"
}

# Function to run dependency checks
check_dependencies() {
    print_header "Dependency Check"
    check_ffmpeg
    check_mirror_server
    print_success "All dependencies are available!"
    echo
    print_status "You can now start streaming with:"
    echo "  $0 stream"
    echo
    print_status "Access the visualization UI at:"
    echo "  https://localhost:8081"
}

# Main script logic
main() {
    local command="${1:-stream}"
    local content="${2:-testsrc}"
    local profile="${3:-high}"
    
    case "$command" in
        "stream")
            check_dependencies
            stream_srt "$content" "$profile"
            ;;
        "check")
            check_dependencies
            ;;
        "content")
            show_content_types
            ;;
        "profiles")
            show_profiles
            ;;
        "help"|"-h"|"--help")
            show_usage
            ;;
        *)
            print_error "Unknown command: $command"
            echo
            show_usage
            exit 1
            ;;
    esac
}

# Handle Ctrl+C gracefully
trap 'print_warning "\nStream stopped by user"; exit 0' INT

# Run main function with all arguments
main "$@"