#!/usr/bin/env bash
# Install everything Aagasa needs, on Ubuntu/Debian or Fedora.
#
#   scripts/install-dependencies.sh            # everything
#   scripts/install-dependencies.sh --server   # control plane only
#   scripts/install-dependencies.sh --worker   # ground station only
#   scripts/install-dependencies.sh --check    # report, install nothing
#
# It asks for sudo only for the distribution packages. Language toolchains go
# under $HOME, because a station should not need a package maintainer's
# permission to run a supported Go version.
#
# What it cannot do: SatDump is not in either distribution's repositories and
# is not ours to redistribute. It is reported and pointed at, never guessed.

set -uo pipefail

ROLE=all
CHECK_ONLY=false
for argument in "$@"; do
    case "$argument" in
        --server) ROLE=server ;;
        --worker) ROLE=worker ;;
        --all)    ROLE=all ;;
        --check)  CHECK_ONLY=true ;;
        -h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) printf 'unknown option: %s\n' "$argument" >&2; exit 2 ;;
    esac
done

# Versions ---------------------------------------------------------------------
#
# Pinned to what the project is actually built and tested with. Raising one is
# a deliberate change, not something that happens because a mirror moved on.
GO_VERSION=1.27.0
FLUTTER_CHANNEL=stable
MINIMUM_PYTHON=3.11

GO_ROOT="${AAGASA_GO_ROOT:-$HOME/.local/go}"
FLUTTER_ROOT="${AAGASA_FLUTTER_ROOT:-$HOME/.local/flutter}"
LOCAL_BIN="$HOME/.local/bin"

INSTALLED=(); SKIPPED=(); MISSING=()

say()  { printf '\n== %s\n' "$1"; }
note() { printf '   %s\n' "$1"; }
ok()   { printf '   [ok]      %s\n' "$1"; SKIPPED+=("$1"); }
did()  { printf '   [added]   %s\n' "$1"; INSTALLED+=("$1"); }
gap()  { printf '   [missing] %s\n' "$1"; MISSING+=("$1"); }
die()  { printf 'install: %s\n' "$1" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# Distribution -----------------------------------------------------------------

detect_distribution() {
    [ -r /etc/os-release ] || die "cannot read /etc/os-release; unsupported system"
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}${ID_LIKE:-}" in
        *ubuntu*|*debian*) echo apt ;;
        *fedora*|*rhel*|*centos*) echo dnf ;;
        *) die "only Ubuntu/Debian and Fedora are supported here; ID=${ID:-unknown}" ;;
    esac
}

PACKAGE_MANAGER="$(detect_distribution)"
# shellcheck disable=SC1091
. /etc/os-release
say "System"
note "${PRETTY_NAME:-unknown} using $PACKAGE_MANAGER"
note "role: $ROLE"
$CHECK_ONLY && note "checking only; nothing will be installed"

install_packages() {
    local packages=("$@")
    local wanted=()
    for package in "${packages[@]}"; do
        if [ "$PACKAGE_MANAGER" = apt ]; then
            dpkg -s "$package" >/dev/null 2>&1 && { ok "$package"; continue; }
        else
            rpm -q "$package" >/dev/null 2>&1 && { ok "$package"; continue; }
        fi
        wanted+=("$package")
    done
    [ "${#wanted[@]}" -eq 0 ] && return 0

    if $CHECK_ONLY; then
        for package in "${wanted[@]}"; do gap "$package"; done
        return 0
    fi

    note "installing: ${wanted[*]}"
    if [ "$PACKAGE_MANAGER" = apt ]; then
        sudo apt-get update -qq || die "apt-get update failed"
        sudo apt-get install -y "${wanted[@]}" || die "apt-get install failed"
    else
        sudo dnf install -y "${wanted[@]}" || die "dnf install failed"
    fi
    for package in "${wanted[@]}"; do did "$package"; done
}

# Distribution packages ---------------------------------------------------------

say "Base tools"
if [ "$PACKAGE_MANAGER" = apt ]; then
    install_packages git curl wget unzip xz-utils ca-certificates \
        build-essential pkg-config openssl jq
else
    install_packages git curl wget unzip xz ca-certificates \
        gcc gcc-c++ make pkgconf-pkg-config openssl jq
fi

say "Containers"
# Podman runs the datastores in development and the whole deployment in
# production; podman-compose drives the production stack (spec.md section 24).
install_packages podman podman-compose
# Rootless podman needs subuid/subgid ranges; without them every container
# fails with a mapping error that reads like a permissions bug. Checked even
# in check mode, because it is exactly the sort of thing that is missing.
if grep -q "^$USER:" /etc/subuid 2>/dev/null; then
    ok "rootless container id ranges"
elif $CHECK_ONLY; then
    gap "subuid/subgid ranges for $USER"
else
    note "adding subuid/subgid ranges for rootless containers"
    sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 "$USER" \
        && did "rootless container id ranges" \
        || note "could not add id ranges; rootless podman may not work"
fi

say "Protocol buffers"
# `make proto` regenerates the Go and Python stubs; the compiler comes from the
# distribution, the language plugins from Go.
if [ "$PACKAGE_MANAGER" = apt ]; then
    install_packages protobuf-compiler
else
    install_packages protobuf-compiler
fi

say "Python"
if [ "$PACKAGE_MANAGER" = apt ]; then
    install_packages python3 python3-venv python3-pip
else
    install_packages python3 python3-pip
fi
if have python3; then
    version="$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')"
    if [ "$(printf '%s\n%s\n' "$MINIMUM_PYTHON" "$version" | sort -V | head -1)" = "$MINIMUM_PYTHON" ]; then
        ok "python $version (>= $MINIMUM_PYTHON)"
    else
        gap "python $version is older than $MINIMUM_PYTHON; uv will fetch its own"
    fi
fi

if [ "$ROLE" != server ]; then
    say "Ground station hardware"
    # rtl_test enumerates the dongle; the Worker shells out to it by name.
    if [ "$PACKAGE_MANAGER" = apt ]; then
        install_packages rtl-sdr librtlsdr-dev
    else
        install_packages rtl-sdr rtl-sdr-devel
    fi

    # The rotator is a USB serial device; without group membership the Worker
    # cannot open it and reports the port as unavailable.
    if id -nG "$USER" | tr ' ' '\n' | grep -qx dialout; then
        ok "$USER is in the dialout group"
    elif $CHECK_ONLY; then
        gap "$USER is not in the dialout group; the rotator port will not open"
    else
        sudo usermod -aG dialout "$USER" \
            && did "$USER added to dialout (log out and back in for it to apply)" \
            || note "could not add $USER to dialout; the serial port will not open"
    fi

    # The dongle is claimed by the DVB-T kernel driver on a stock system, which
    # stops SatDump from opening it at all.
    blacklist=/etc/modprobe.d/aagasa-rtlsdr.conf
    if [ -f "$blacklist" ]; then
        ok "dvb_usb_rtl28xxu blacklisted"
    elif $CHECK_ONLY; then
        gap "dvb_usb_rtl28xxu is not blacklisted; the DVB-T driver will claim the dongle"
    else
        printf '# Aagasa: keep the DVB-T driver off the SDR dongle.\nblacklist dvb_usb_rtl28xxu\n' \
            | sudo tee "$blacklist" >/dev/null \
            && did "dvb_usb_rtl28xxu blacklisted (reboot or rmmod to apply)" \
            || note "could not write $blacklist"
    fi
fi

# Toolchains under $HOME ---------------------------------------------------------

say "Go $GO_VERSION"
if have go && [ "$(go env GOVERSION 2>/dev/null)" = "go$GO_VERSION" ]; then
    ok "go$GO_VERSION"
elif $CHECK_ONLY; then
    gap "go$GO_VERSION (found: $(go version 2>/dev/null || echo none))"
else
    architecture="$(uname -m)"
    case "$architecture" in
        x86_64) architecture=amd64 ;;
        aarch64) architecture=arm64 ;;
        *) die "unsupported architecture for the Go tarball: $architecture" ;;
    esac
    archive="go${GO_VERSION}.linux-${architecture}.tar.gz"
    note "downloading $archive"
    temporary="$(mktemp -d)"
    if curl -fsSL "https://go.dev/dl/$archive" -o "$temporary/$archive"; then
        rm -rf "$GO_ROOT"
        mkdir -p "$(dirname "$GO_ROOT")"
        tar -C "$temporary" -xzf "$temporary/$archive" && mv "$temporary/go" "$GO_ROOT" \
            && did "go$GO_VERSION in $GO_ROOT" || die "could not unpack Go"
    else
        die "could not download Go $GO_VERSION"
    fi
    rm -rf "$temporary"
fi

say "uv"
if have uv; then
    ok "uv $(uv --version 2>/dev/null | awk '{print $2}')"
elif $CHECK_ONLY; then
    gap "uv"
else
    # uv manages both Python projects and will fetch a suitable interpreter
    # itself if the system one is too old.
    curl -fsSL https://astral.sh/uv/install.sh | sh >/dev/null 2>&1 \
        && did "uv in $LOCAL_BIN" || die "could not install uv"
fi

if [ "$ROLE" != worker ]; then
    say "Flutter"
    if have flutter; then
        ok "flutter $(flutter --version 2>/dev/null | head -1 | awk '{print $2}')"
    elif [ -x "$FLUTTER_ROOT/bin/flutter" ]; then
        ok "flutter in $FLUTTER_ROOT"
    elif $CHECK_ONLY; then
        gap "flutter"
    else
        note "cloning the $FLUTTER_CHANNEL channel into $FLUTTER_ROOT"
        mkdir -p "$(dirname "$FLUTTER_ROOT")"
        git clone --depth 1 -b "$FLUTTER_CHANNEL" \
            https://github.com/flutter/flutter.git "$FLUTTER_ROOT" >/dev/null 2>&1 \
            && did "flutter ($FLUTTER_CHANNEL) in $FLUTTER_ROOT" \
            || die "could not clone Flutter"
    fi

    # Building the Linux desktop target needs these; the web build does not.
    # Installed anyway so `flutter doctor` is clean and either target works.
    say "Flutter Linux build tools"
    if [ "$PACKAGE_MANAGER" = apt ]; then
        install_packages clang cmake ninja-build libgtk-3-dev liblzma-dev
    else
        install_packages clang cmake ninja-build gtk3-devel xz-devel
    fi
fi

# Go-installed protobuf plugins --------------------------------------------------

say "Protobuf plugins"
export PATH="$GO_ROOT/bin:$HOME/go/bin:$LOCAL_BIN:$PATH"
if have protoc-gen-go && have protoc-gen-go-grpc; then
    ok "protoc-gen-go and protoc-gen-go-grpc"
elif $CHECK_ONLY; then
    gap "protoc-gen-go, protoc-gen-go-grpc"
elif have go; then
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest >/dev/null 2>&1 \
        && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest >/dev/null 2>&1 \
        && did "protoc-gen-go and protoc-gen-go-grpc in $HOME/go/bin" \
        || note "could not install the protobuf plugins; run 'make proto' to see why"
else
    gap "protoc-gen-go (needs Go on PATH)"
fi

# SatDump ------------------------------------------------------------------------

if [ "$ROLE" != server ]; then
    say "SatDump"
    if have satdump; then
        # SatDump has no --version flag and answers with a usage error, so
        # report where it is rather than inventing a version string.
        ok "satdump at $(command -v satdump)"
    else
        gap "satdump"
        note "SatDump is not in either distribution's repositories and is not"
        note "ours to redistribute. For the containerised deployment, download"
        note "the .deb release package for the target architecture to"
        note "deployment/worker/satdump.deb (deployment/README.md). Without it"
        note "the Worker image cannot be built and the station cannot record."
    fi
fi

# PATH -----------------------------------------------------------------------------

if ! $CHECK_ONLY; then
    say "PATH"
    profile="$HOME/.profile"
    [ -f "$HOME/.bashrc" ] && profile="$HOME/.bashrc"
    [ -n "${ZSH_VERSION:-}" ] && profile="$HOME/.zshrc"

    if grep -q "# Aagasa toolchain" "$profile" 2>/dev/null; then
        ok "$profile already sets the toolchain PATH"
    else
        {
            printf '\n# Aagasa toolchain\n'
            printf 'export PATH="%s/bin:$HOME/go/bin:%s:%s/bin:$PATH"\n' \
                "$GO_ROOT" "$LOCAL_BIN" "$FLUTTER_ROOT"
        } >> "$profile"
        did "toolchain PATH added to $profile"
        note "run: source $profile"
    fi
fi

# Verify --------------------------------------------------------------------------

say "Summary"
printf '   already present: %d\n' "${#SKIPPED[@]}"
printf '   installed:       %d\n' "${#INSTALLED[@]}"
printf '   still missing:   %d\n' "${#MISSING[@]}"
if [ "${#MISSING[@]}" -gt 0 ]; then
    for item in "${MISSING[@]}"; do note "missing: $item"; done
fi

cat <<NEXT

Next:
  source ${profile:-~/.bashrc}
  make test                      # unit tests, no databases or hardware needed
  make build && ./deploy.sh both # the whole stack on this host, real devices

Deployment needs one more thing that package managers cannot supply: download
the SatDump release package for this architecture to deployment/worker/satdump.deb
(deployment/README.md).

If you are on the station host, plug in the rotator and the dongle and check
them with:
  cd worker && uv run aagasa-hardware status

A dialout group change and the DVB-T blacklist both need a fresh login (and
the blacklist a reboot, or 'sudo rmmod dvb_usb_rtl28xxu') before they apply.
NEXT

[ "${#MISSING[@]}" -eq 0 ]
