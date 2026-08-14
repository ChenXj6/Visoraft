ARG FFMPEG_VERSION=8.1.2
ARG FFMPEG_RELEASE_FINGERPRINT=FCF986EA15E6E293A5644F10B4322F04D67658D8
ARG OPENH264_VERSION=2.6.0
ARG OPENH264_SHA256=558544AD358283A7AB2930D69A9CEDDF913F4A51EE9BF1BFB9E377322AF81A69

FROM python:3.12-slim-bookworm AS ffmpeg-build

ARG FFMPEG_VERSION
ARG FFMPEG_RELEASE_FINGERPRINT
ARG OPENH264_VERSION
ARG OPENH264_SHA256

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        gnupg \
        libass-dev \
        nasm \
        pkg-config \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /work

RUN curl --fail --show-error --silent --location \
        --retry 5 --retry-all-errors \
        "https://codeload.github.com/cisco/openh264/tar.gz/refs/tags/v${OPENH264_VERSION}" \
        --output "openh264-${OPENH264_VERSION}.tar.gz" \
    && printf "%s  %s\n" \
        "${OPENH264_SHA256}" \
        "openh264-${OPENH264_VERSION}.tar.gz" \
        | sha256sum --check --strict \
    && tar --extract --file "openh264-${OPENH264_VERSION}.tar.gz"

WORKDIR /work/openh264-${OPENH264_VERSION}

RUN make -j"$(nproc)" \
    && make PREFIX=/opt/openh264 install-shared \
    && test -s /opt/openh264/lib/libopenh264.so \
    && test -s /opt/openh264/lib/pkgconfig/openh264.pc

RUN install -d /opt/openh264-compliance/source /opt/openh264-compliance/build \
    && cp /work/openh264-${OPENH264_VERSION}.tar.gz \
        /opt/openh264-compliance/source/ \
    && cp LICENSE /opt/openh264-compliance/ \
    && printf "Release URL: https://github.com/cisco/openh264/releases/tag/v%s\nCommit: 652bdb7719f30b52b08e506645a7322ff1b2cc6f\nSHA-256: %s\nInstall target: install-shared\n" \
        "${OPENH264_VERSION}" \
        "${OPENH264_SHA256}" \
        > /opt/openh264-compliance/build/provenance.txt

WORKDIR /work

RUN curl --fail --show-error --silent --location \
        --retry 5 --retry-all-errors \
        "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" \
        --output "ffmpeg-${FFMPEG_VERSION}.tar.xz" \
    && curl --fail --show-error --silent --location \
        --retry 5 --retry-all-errors \
        "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz.asc" \
        --output "ffmpeg-${FFMPEG_VERSION}.tar.xz.asc" \
    && curl --fail --show-error --silent --location \
        --retry 5 --retry-all-errors \
        "https://ffmpeg.org/ffmpeg-devel.asc" \
        --output ffmpeg-devel.asc \
    && install -d -m 0700 /tmp/ffmpeg-gnupg \
    && gpg --batch --homedir /tmp/ffmpeg-gnupg --import ffmpeg-devel.asc \
    && test "$(gpg --batch --homedir /tmp/ffmpeg-gnupg --with-colons --fingerprint "${FFMPEG_RELEASE_FINGERPRINT}" | awk -F: '$1 == "fpr" { print $10; exit }')" = "${FFMPEG_RELEASE_FINGERPRINT}" \
    && gpg --batch --homedir /tmp/ffmpeg-gnupg \
        --verify "ffmpeg-${FFMPEG_VERSION}.tar.xz.asc" "ffmpeg-${FFMPEG_VERSION}.tar.xz" \
    && sha256sum "ffmpeg-${FFMPEG_VERSION}.tar.xz" \
        > "ffmpeg-${FFMPEG_VERSION}.tar.xz.sha256" \
    && tar --extract --file "ffmpeg-${FFMPEG_VERSION}.tar.xz"

WORKDIR /work/ffmpeg-${FFMPEG_VERSION}

# No --enable-gpl, --enable-nonfree, --enable-version3, or GPL/nonfree codec.
# Autodetection and network protocols stay disabled. Only the explicitly pinned
# OpenH264 encoder and dynamically linked libass subtitle filter are added.
RUN PKG_CONFIG_PATH=/opt/openh264/lib/pkgconfig \
    ./configure \
        --prefix=/opt/ffmpeg \
        --disable-autodetect \
        --disable-debug \
        --disable-doc \
        --disable-ffplay \
        --disable-network \
        --disable-static \
        --enable-libass \
        --enable-libopenh264 \
        --enable-pic \
        --enable-shared \
        --extra-cflags="-I/opt/openh264/include" \
        --extra-ldflags="-L/opt/openh264/lib -Wl,-rpath,/opt/ffmpeg/lib -Wl,-rpath,/opt/openh264/lib" \
        --pkg-config-flags="--define-prefix" \
    && make -j"$(nproc)" \
    && make install

RUN install -d /opt/ffmpeg-compliance/source /opt/ffmpeg-compliance/build \
    && cp /work/ffmpeg-${FFMPEG_VERSION}.tar.xz \
        /work/ffmpeg-${FFMPEG_VERSION}.tar.xz.asc \
        /work/ffmpeg-${FFMPEG_VERSION}.tar.xz.sha256 \
        /work/ffmpeg-devel.asc \
        /opt/ffmpeg-compliance/source/ \
    && cp COPYING.LGPLv2.1 /opt/ffmpeg-compliance/ \
    && printf "Release URL: https://ffmpeg.org/releases/ffmpeg-%s.tar.xz\nRelease signing key: %s\n\n" "${FFMPEG_VERSION}" "${FFMPEG_RELEASE_FINGERPRINT}" \
        > /opt/ffmpeg-compliance/build/provenance.txt \
    && /opt/ffmpeg/bin/ffmpeg -hide_banner -version \
        >> /opt/ffmpeg-compliance/build/provenance.txt \
    && printf "\n" >> /opt/ffmpeg-compliance/build/provenance.txt \
    && /opt/ffmpeg/bin/ffmpeg -hide_banner -buildconf \
        >> /opt/ffmpeg-compliance/build/provenance.txt \
    && /opt/ffmpeg/bin/ffmpeg -hide_banner -buildconf \
        > /opt/ffmpeg-compliance/build/buildconf.txt 2>&1 \
    && /opt/ffmpeg/bin/ffmpeg -hide_banner -L \
        > /opt/ffmpeg-compliance/build/license.txt 2>&1 \
    && /opt/ffmpeg/bin/ffprobe -version \
        > /opt/ffmpeg-compliance/build/ffprobe-version.txt 2>&1 \
    && test -s /opt/ffmpeg-compliance/build/ffprobe-version.txt

RUN set -eux \
    && grep -q -- "--disable-autodetect" /opt/ffmpeg-compliance/build/buildconf.txt \
    && grep -q -- "--disable-network" /opt/ffmpeg-compliance/build/buildconf.txt \
    && grep -q -- "--enable-shared" /opt/ffmpeg-compliance/build/buildconf.txt \
    && ! grep -q -- "--enable-gpl" /opt/ffmpeg-compliance/build/buildconf.txt \
    && ! grep -q -- "--enable-nonfree" /opt/ffmpeg-compliance/build/buildconf.txt \
    && ! grep -q -- "--enable-version3" /opt/ffmpeg-compliance/build/buildconf.txt \
    && grep -q -- "--enable-libopenh264" /opt/ffmpeg-compliance/build/buildconf.txt \
    && grep -q -- "--enable-libass" /opt/ffmpeg-compliance/build/buildconf.txt \
    && grep -q "GNU Lesser General Public" \
        /opt/ffmpeg-compliance/build/license.txt \
    && grep -q "version 2.1 of the License" \
        /opt/ffmpeg-compliance/build/license.txt \
    && /opt/ffmpeg/bin/ffmpeg -hide_banner -encoders \
        > /opt/ffmpeg-compliance/build/encoders.txt 2>&1 \
    && /opt/ffmpeg/bin/ffmpeg -hide_banner -filters \
        > /opt/ffmpeg-compliance/build/filters.txt 2>&1 \
    && grep -q "libopenh264" \
        /opt/ffmpeg-compliance/build/encoders.txt \
    && grep -q " subtitles " \
        /opt/ffmpeg-compliance/build/filters.txt \
    && ! grep -Eq "libx26[45]" \
        /opt/ffmpeg-compliance/build/encoders.txt \
    && /opt/ffmpeg/bin/ffmpeg \
        -hide_banner \
        -loglevel error \
        -f lavfi \
        -i "color=c=black:s=320x240:d=0.1" \
        -frames:v 1 \
        -pix_fmt yuv420p \
        -c:v libopenh264 \
        -f h264 \
        -y /tmp/openh264-smoke.h264 \
    && test -s /tmp/openh264-smoke.h264 \
    && rm -f /tmp/openh264-smoke.h264

FROM python:3.12-slim-bookworm

ARG FFMPEG_VERSION
ARG FFMPEG_RELEASE_FINGERPRINT
ARG OPENH264_VERSION
ARG OPENH264_SHA256

LABEL org.opencontainers.image.title="Visoraft Media Worker" \
      org.opencontainers.image.licenses="Apache-2.0 AND LGPL-2.1-or-later AND BSD-2-Clause AND ISC AND OFL-1.1" \
      io.visoraft.ffmpeg.version="${FFMPEG_VERSION}" \
      io.visoraft.ffmpeg.release-key="${FFMPEG_RELEASE_FINGERPRINT}" \
      io.visoraft.ffmpeg.gpl-enabled="false" \
      io.visoraft.ffmpeg.nonfree-enabled="false" \
      io.visoraft.openh264.version="${OPENH264_VERSION}" \
      io.visoraft.openh264.sha256="${OPENH264_SHA256}"

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PATH=/opt/ffmpeg/bin:$PATH \
    LD_LIBRARY_PATH=/opt/ffmpeg/lib:/opt/openh264/lib

RUN groupadd --system --gid 10001 visoraft \
    && useradd --system --uid 10001 --gid visoraft --create-home visoraft

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        fontconfig \
        fonts-noto-cjk \
        libass9 \
        tesseract-ocr \
        tesseract-ocr-chi-sim \
        tesseract-ocr-chi-tra \
    && install -d /usr/share/media-compliance/libass \
        /usr/share/media-compliance/fonts-noto-cjk \
        /usr/share/media-compliance/tesseract \
        /usr/share/media-compliance/tessdata-chi-sim \
        /usr/share/media-compliance/tessdata-chi-tra \
    && cp /usr/share/doc/libass9/copyright \
        /usr/share/media-compliance/libass/copyright \
    && cp /usr/share/doc/fonts-noto-cjk/copyright \
        /usr/share/media-compliance/fonts-noto-cjk/copyright \
    && cp /usr/share/doc/tesseract-ocr/copyright \
        /usr/share/media-compliance/tesseract/copyright \
    && cp /usr/share/doc/tesseract-ocr-chi-sim/copyright \
        /usr/share/media-compliance/tessdata-chi-sim/copyright \
    && cp /usr/share/doc/tesseract-ocr-chi-tra/copyright \
        /usr/share/media-compliance/tessdata-chi-tra/copyright \
    && rm -rf /var/lib/apt/lists/*

COPY --from=ffmpeg-build /opt/ffmpeg /opt/ffmpeg
COPY --from=ffmpeg-build /opt/ffmpeg-compliance /usr/share/ffmpeg-compliance
COPY --from=ffmpeg-build /opt/openh264 /opt/openh264
COPY --from=ffmpeg-build /opt/openh264-compliance /usr/share/openh264-compliance

WORKDIR /app
COPY pyproject.toml ./
COPY src ./src
RUN --mount=type=cache,target=/root/.cache/pip \
    python -m pip install --timeout 300 --retries 10 . \
    && deno --version \
    && python -c "import importlib.metadata as m; assert m.version('yt-dlp-ejs') == '0.8.0'" \
    && ffmpeg -hide_banner -version \
    && ffprobe -hide_banner -version \
    && tesseract --version \
    && tesseract --list-langs 2>&1 | grep -q "chi_sim" \
    && tesseract --list-langs 2>&1 | grep -q "chi_tra" \
    && ffmpeg -hide_banner -encoders | grep -q "libopenh264" \
    && ffmpeg -hide_banner -filters | grep -q " subtitles " \
    && test -r /usr/share/ffmpeg-compliance/build/provenance.txt \
    && test -r /usr/share/openh264-compliance/build/provenance.txt \
    && test -r /usr/share/openh264-compliance/LICENSE \
    && test -r /usr/share/media-compliance/tesseract/copyright \
    && test -r /usr/share/media-compliance/tessdata-chi-sim/copyright \
    && test -r /usr/share/media-compliance/tessdata-chi-tra/copyright \
    && printf "WEBVTT\n\n00:00:00.000 --> 00:00:00.200\nVisoraft\n" \
        > /tmp/subtitle-smoke.vtt \
    && ffmpeg \
        -hide_banner \
        -loglevel error \
        -f lavfi \
        -i "color=c=black:s=320x240:d=0.3" \
        -vf "subtitles=filename=/tmp/subtitle-smoke.vtt" \
        -frames:v 2 \
        -pix_fmt yuv420p \
        -c:v libopenh264 \
        -f h264 \
        -y /tmp/subtitle-smoke.h264 \
    && test -s /tmp/subtitle-smoke.h264 \
    && rm -f /tmp/subtitle-smoke.vtt /tmp/subtitle-smoke.h264

USER 10001:10001

CMD ["python", "-m", "visoraft_media.worker"]
