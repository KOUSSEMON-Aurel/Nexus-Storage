#!/bin/bash
set -e

# Use rustup's toolchain instead of system rustc
export RUSTUP_TOOLCHAIN=stable
export PATH="$HOME/.rustup/toolchains/stable-x86_64-unknown-linux-gnu/bin:$PATH"

NDK="/home/aurel/Android/Sdk/ndk/28.2.13676358"
TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin"
export ANDROID_NDK_HOME="$NDK"

# Ensure all targets are installed
echo "📥 Ensuring Rust targets are installed..."
rustup target add aarch64-linux-android armv7-linux-androideabi x86_64-linux-android i686-linux-android

build_target() {
    local ARCH=$1
    local TARGET=$2
    local COMPILER_PREFIX=$3
    local API_LEVEL=24
    
    echo "🦀 Compiling nexus-core → Android $ARCH (release)..."

    local CC="$TOOLCHAIN/${COMPILER_PREFIX}${API_LEVEL}-clang"
    local AR="$TOOLCHAIN/llvm-ar"
    local JNILIBS="/home/aurel/CODE/Nexus-Storage/nexus-mobile/android/app/src/main/jniLibs/$ARCH"

    export CARGO_TARGET_$(echo $TARGET | tr '-' '_' | tr '[:lower:]' '[:upper:]')_LINKER="$CC"
    export "CC_$(echo $TARGET | tr '-' '_')"="$CC"
    export "AR_$(echo $TARGET | tr '-' '_')"="$AR"

    cargo build \
      --target "$TARGET" \
      --package nexus-core \
      --release

    echo "📦 Copying .so to jniLibs/$ARCH..."
    mkdir -p "$JNILIBS"
    cp "target/$TARGET/release/libnexus_core.so" "$JNILIBS/libnexus_core.so"
    
    echo "✅ libnexus_core.so ready in $JNILIBS"
}

# Build common architectures first (important for current devices and emulators)
build_target "x86_64" "x86_64-linux-android" "x86_64-linux-android"
build_target "arm64-v8a" "aarch64-linux-android" "aarch64-linux-android"

# Try to build 32-bit architectures (may need extra flags or be skipped if incompatible)
echo "🦀 Compiling nexus-core → Android armeabi-v7a (with NEON fix)..."
RUSTFLAGS="-C target-feature=+neon" build_target "armeabi-v7a" "armv7-linux-androideabi" "armv7a-linux-androideabi"

build_target "x86" "i686-linux-android" "i686-linux-android"

echo "✨ Multi-arch Rust build complete!"

