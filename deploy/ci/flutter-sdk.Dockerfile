# Flutter SDK 镜像 —— 客户端 CI 构建 (GitLab CI docker executor) 用。
#
# 为什么自建: cirrusci/cirruslabs 的 Flutter 镜像最高停在 3.29 / Dart 3.7,
# 满足不了 pubspec 要的 Flutter >=3.27.0 / Dart ^3.11.5 (开发机用 3.41.9)。
# 自建一次推 GitLab Container Registry, 后续 CI 直接拉, 不重复装工具链,
# 且 docker executor 进程级隔离 build-host 的 Sonic/Nacos/etc → 不再有
# Jenkins durable-task "process apparently never started" 那类宿主干扰。
#
# 覆盖平台: Android (apk/aab) + Linux desktop (AppImage/deb) —— 这两个 Linux
# 平台共用此镜像。macOS/iOS 走 Mac runner (shell executor, 不用此镜像);
# Windows 走 Windows runner。见 .gitlab-ci.yml build job 矩阵。
#
# 内网构建注意: git clone flutter (github.com) + sdkmanager (dl.google.com)
# 需外网。build-host 内网不通时, build 前 export HTTP_PROXY/HTTPS_PROXY 再
# docker build, 或换内网镜像源 (build-arg 覆盖):
#   docker build \
#     --build-arg UBUNTU_IMAGE=${INFRA_REGISTRY}/library/ubuntu:22.04 \
#     --build-arg FLUTTER_GIT_URL=https://<内网 git 镜像>/flutter/flutter.git \
#     -t $CI_REGISTRY/biumind/flutter-sdk:${FLUTTER_VERSION} \
#     -f deploy/ci/flutter-sdk.Dockerfile .
#
# 构建上下文是仓库根 (跟其它 service Dockerfile 一致; 本 Dockerfile 不 COPY
# 任何 repo 文件, 但留 root context 以便将来扩展)。

# 基础镜像可配 (内网 mirror): 默认 docker.io, 内网用 INFRA_REGISTRY 覆盖。
ARG UBUNTU_IMAGE=docker.io/library/ubuntu:22.04
FROM ${UBUNTU_IMAGE}

# 工具链版本可配 (随 Flutter / Android 平台升级漂移时改这里, 不改 Dockerfile 主体)
ARG FLUTTER_VERSION=3.41.9
ARG FLUTTER_GIT_URL=https://github.com/flutter/flutter.git
ARG ANDROID_CMDLINE_TOOLS=11076708
ARG ANDROID_API_LEVEL=36
ARG ANDROID_BUILD_TOOLS=36.0.0

ENV DEBIAN_FRONTEND=noninteractive

ENV FLUTTER_HOME=/opt/flutter
ENV ANDROID_HOME=/opt/android-sdk
ENV ANDROID_SDK_ROOT=${ANDROID_HOME}
# PATH 让 flutter / dart / sdkmanager / adb 直接可用 (CI script 里不用再 export)。
ENV PATH=${FLUTTER_HOME}/bin:${FLUTTER_HOME}/bin/cache/dart-sdk/bin:${ANDROID_HOME}/cmdline-tools/latest/bin:${ANDROID_HOME}/platform-tools:${PATH}

# ── 系统依赖 ──
# JDK 17 (Gradle 8.14 / Kotlin 2.2.20 / AGP 要); Linux desktop (flutter build
# linux 要 clang/cmake/ninja/gtk); 通用工具 (git/curl/unzip/zip)。
RUN apt-get update && apt-get install -y --no-install-recommends \
      openjdk-17-jdk \
      ca-certificates curl git unzip wget xz-utils zip gnupg2 procps \
      clang cmake ninja-build pkg-config \
      libgtk-3-dev liblzma-dev libstdc++-12-dev libgcrypt20-dev \
    && rm -rf /var/lib/apt/lists/*

ENV JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64

# ── Flutter SDK (git 浅克隆指定 tag, 与开发机完全一致) ──
# 不在镜像里跑 flutter doctor —— 它对缺 iOS/Chrome 等无关项会报 issue, 可能
# 返回非 0 导致镜像 build 失败。doctor 在 CI check job 里跑 (容忍 warning)。
# precache 预下 android + linux engine artifacts (加速首次 build); 不预下
# ios/macos/windows/web (本镜像不做这些平台)。
RUN git clone --depth 1 --branch ${FLUTTER_VERSION} \
      ${FLUTTER_GIT_URL} ${FLUTTER_HOME} \
    && flutter --version \
    && flutter precache --android --linux --no-ios --no-macos --no-windows --no-web

# ── Android SDK (命令行版, 不需 Android Studio) ──
# cmdline-tools 解包到 cmdline-tools/latest/ (sdkmanager 要求这个目录结构,
# 否则 sdkmanager 找不到自己的 source.properties)。
RUN mkdir -p ${ANDROID_HOME}/cmdline-tools \
    && cd ${ANDROID_HOME}/cmdline-tools \
    && wget -q https://dl.google.com/android/repository/commandlinetools-linux-${ANDROID_CMDLINE_TOOLS}_latest.zip -O cmdline-tools.zip \
    && unzip -q cmdline-tools.zip && rm cmdline-tools.zip \
    && mv cmdline-tools latest \
    && yes | sdkmanager --licenses > /dev/null \
    && sdkmanager \
      "platform-tools" \
      "platforms;android-${ANDROID_API_LEVEL}" \
      "build-tools;${ANDROID_BUILD_TOOLS}" \
    && sdkmanager --list_installed

# GitLab CI docker executor 把仓库 checkout 到 /builds/<group>/<project>, 容器
# WORKDIR 不影响 runner 行为; 这里只给手动 `docker run -it` 测试一个合理目录。
WORKDIR /builds

# 镜像元信息 (build 时 ARG 会被 bake 进 layer, 这里再标一次方便 docker inspect)
LABEL org.opencontainers.image.title="biumind/flutter-sdk" \
      org.opencontainers.image.description="Flutter ${FLUTTER_VERSION} + JDK17 + Android SDK (api ${ANDROID_API_LEVEL}) + Linux desktop deps — BiuMind 客户端 CI 用" \
      org.opencontainers.image.source="deploy/ci/flutter-sdk.Dockerfile"
