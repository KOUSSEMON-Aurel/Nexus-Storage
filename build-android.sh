#!/bin/bash
set -e

# Use rustup's toolchain instead of system rustc
export RUSTUP_TOOLCHAIN=stable
export PATH="$HOME/.rustup/toolchains/stable-x86_64-unknown-linux-gnu/bin:$PATH"

NDK="/home/aurel/Android/Sdk/ndk/28.2.13676358"
TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin"
CC="$TOOLCHAIN/aarch64-linux-android24-clang"
AR="$TOOLCHAIN/llvm-ar"
TARGET="aarch64-linux-android"
JNILIBS="/home/aurel/CODE/Nexus-Storage/nexus-mobile/android/app/src/main/jniLibs/arm64-v8a"

echo "🦀 Compilation nexus-core → Android arm64 (release)..."

export ANDROID_NDK_HOME="$NDK"
export CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER="$CC"
export CC_aarch64_linux_android="$CC"
export AR_aarch64_linux_android="$AR"

cargo build \
  --target "$TARGET" \
  --package nexus-core \
  --release

echo "📦 Copie de la .so vers jniLibs..."
mkdir -p "$JNILIBS"
cp "target/$TARGET/release/libnexus_core.so" "$JNILIBS/libnexus_core.so"

echo "✅ libnexus_core.so prête dans $JNILIBS"
nm -D "$JNILIBS/libnexus_core.so" | grep -c "nexus_" && echo "Symboles exportés ↑"
