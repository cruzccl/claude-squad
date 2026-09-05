#!/usr/bin/env bash

set -e

# This is a personal fork with no published releases, so there is nothing to download:
# the script builds the checked-out source and installs the resulting binary.

resolve_repo_root() {
    # The source has to be on disk to build it, which rules out `curl ... | bash`.
    if [ -z "${BASH_SOURCE[0]}" ] || [ ! -f "${BASH_SOURCE[0]}" ]; then
        err "install.sh builds from source, so it has to be run from a clone of the repository:
    git clone https://github.com/cruzccl/claude-squad.git
    cd claude-squad && ./install.sh"
    fi

    REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

    if [ ! -f "$REPO_ROOT/go.mod" ]; then
        err "no go.mod in $REPO_ROOT — install.sh must sit at the root of the repository."
    fi
}

setup_shell_and_path() {
    BIN_DIR=${BIN_DIR:-$HOME/.local/bin}

    case $SHELL in
        */zsh)
            PROFILE=$HOME/.zshrc
            ;;
        */bash)
            PROFILE=$HOME/.bashrc
            ;;
        */fish)
            PROFILE=$HOME/.config/fish/config.fish
            ;;
        */ash)
            PROFILE=$HOME/.profile
            ;;
        *)
            echo "could not detect shell, manually add ${BIN_DIR} to your PATH."
            exit 1
    esac

    if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
        echo >> "$PROFILE" && echo "export PATH=\"\$PATH:$BIN_DIR\"" >> "$PROFILE"
    fi
}

detect_platform() {
    # Only the platform is needed now: the Go toolchain picks the architecture itself.
    PLATFORM="$(uname | tr '[:upper:]' '[:lower:]')"
    if [[ "$PLATFORM" == mingw*_nt* ]]; then
        PLATFORM="windows"
        EXTENSION=".exe"
    else
        EXTENSION=""
    fi
}

check_command_exists() {
    if command -v "$INSTALL_NAME" &> /dev/null; then
        EXISTING_PATH=$(which "$INSTALL_NAME")
        echo "Found existing installation of '$INSTALL_NAME' at $EXISTING_PATH"
        echo "Will rebuild from the current source"
        UPGRADE_MODE=true
    else
        UPGRADE_MODE=false
    fi
}

check_and_install_dependencies() {
    echo "Checking for required dependencies..."

    # Check for the Go toolchain. Unlike tmux and gh this is not installed automatically:
    # distribution packages are often older than the version go.mod asks for, and picking
    # a toolchain is the kind of decision to leave to whoever runs this.
    if ! command -v go &> /dev/null; then
        err "Go is not installed, and it is needed to build from source.
Install it from https://go.dev/dl/ and run this script again."
    fi
    echo "Go is already installed ($(go version))."

    # Check for tmux
    if ! command -v tmux &> /dev/null; then
        echo "tmux is not installed. Installing tmux..."

        if [[ "$PLATFORM" == "darwin" ]]; then
            # macOS
            if command -v brew &> /dev/null; then
                ensure brew install tmux
            else
                echo "Homebrew is not installed. Please install Homebrew first to install tmux."
                echo "Visit https://brew.sh for installation instructions."
                exit 1
            fi
        elif [[ "$PLATFORM" == "linux" ]]; then
            # Linux
            if command -v apt-get &> /dev/null; then
                ensure sudo apt-get update
                ensure sudo apt-get install -y tmux
            elif command -v dnf &> /dev/null; then
                ensure sudo dnf install -y tmux
            elif command -v yum &> /dev/null; then
                ensure sudo yum install -y tmux
            elif command -v pacman &> /dev/null; then
                ensure sudo pacman -S --noconfirm tmux
            else
                echo "Could not determine package manager. Please install tmux manually."
                exit 1
            fi
        elif [[ "$PLATFORM" == "windows" ]]; then
            echo "For Windows, please install tmux via WSL or another method."
            exit 1
        fi

        echo "tmux installed successfully."
    else
        echo "tmux is already installed."
    fi

    # Check for GitHub CLI (gh)
    if ! command -v gh &> /dev/null; then
        echo "GitHub CLI (gh) is not installed. Installing GitHub CLI..."

        if [[ "$PLATFORM" == "darwin" ]]; then
            # macOS
            if command -v brew &> /dev/null; then
                ensure brew install gh
            else
                echo "Homebrew is not installed. Please install Homebrew first to install GitHub CLI."
                echo "Visit https://brew.sh for installation instructions."
                exit 1
            fi
        elif [[ "$PLATFORM" == "linux" ]]; then
            # Linux
            if command -v apt-get &> /dev/null; then
                echo "Installing GitHub CLI on Debian/Ubuntu..."
                ensure curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
                ensure sudo chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg
                ensure echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null
                ensure sudo apt-get update
                ensure sudo apt-get install -y gh
            elif command -v dnf &> /dev/null; then
                ensure sudo dnf install -y 'dnf-command(config-manager)'
                ensure sudo dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
                ensure sudo dnf install -y gh
            elif command -v yum &> /dev/null; then
                ensure sudo yum install -y yum-utils
                ensure sudo yum-config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
                ensure sudo yum install -y gh
            elif command -v pacman &> /dev/null; then
                ensure sudo pacman -S --noconfirm github-cli
            else
                echo "Could not determine package manager. Please install GitHub CLI manually."
                echo "Visit https://github.com/cli/cli#installation for installation instructions."
                exit 1
            fi
        elif [[ "$PLATFORM" == "windows" ]]; then
            echo "For Windows, please install GitHub CLI manually."
            echo "Visit https://github.com/cli/cli#installation for installation instructions."
            exit 1
        fi

        echo "GitHub CLI (gh) installed successfully."
    else
        echo "GitHub CLI (gh) is already installed."
    fi

    echo "All dependencies are installed."
}

build_and_install() {
    local bin_dir=$1
    local extension=$2

    # Build into a temporary directory rather than straight over the installed binary, so
    # a build that fails leaves the working installation alone.
    local tmp_dir
    tmp_dir=$(mktemp -d)
    # Expanded now, not when the trap runs: tmp_dir is local and out of scope by then.
    trap "rm -rf '$tmp_dir'" EXIT

    echo ""
    echo "Building from $REPO_ROOT"
    ensure go build -C "$REPO_ROOT" -o "${tmp_dir}/${INSTALL_NAME}${extension}" .

    if [ ! -d "$bin_dir" ]; then
        mkdir -p "$bin_dir"
    fi

    # Remove the existing binary rather than writing over it: overwriting one that is
    # currently running fails with "text file busy".
    if [ "$UPGRADE_MODE" = true ] && [ -f "$bin_dir/$INSTALL_NAME${extension}" ]; then
        echo "Removing previous installation from $bin_dir/$INSTALL_NAME${extension}"
        rm -f "$bin_dir/$INSTALL_NAME${extension}"
    fi

    ensure mv "${tmp_dir}/${INSTALL_NAME}${extension}" "$bin_dir/$INSTALL_NAME${extension}"

    if [ ! -f "$bin_dir/$INSTALL_NAME${extension}" ]; then
        echo "Installation failed, could not find $bin_dir/$INSTALL_NAME${extension}"
        exit 1
    fi

    chmod +x "$bin_dir/$INSTALL_NAME${extension}"

    echo ""
    if [ "$UPGRADE_MODE" = true ]; then
        echo "Successfully rebuilt '$INSTALL_NAME' as:"
    else
        echo "Installed as '$INSTALL_NAME':"
    fi
    echo "$("$bin_dir/$INSTALL_NAME${extension}" version)"
}

main() {
    # Parse command line arguments
    INSTALL_NAME="cs"
    UPGRADE_MODE=false

    while [[ $# -gt 0 ]]; do
        case $1 in
            --name)
                INSTALL_NAME="$2"
                shift 2
                ;;
            *)
                echo "Unknown option: $1"
                echo "Usage: install.sh [--name <n>]"
                exit 1
                ;;
        esac
    done

    resolve_repo_root
    check_command_exists
    detect_platform

    check_and_install_dependencies

    setup_shell_and_path

    build_and_install "$BIN_DIR" "$EXTENSION"
}

# Run a command that should never fail. If the command fails execution
# will immediately terminate with an error showing the failing
# command.
ensure() {
    if ! "$@"; then err "command failed: $*"; fi
}

err() {
    echo "$1" >&2
    exit 1
}

main "$@" || exit 1
