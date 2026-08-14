# FFmpeg / FFprobe LGPL 构建边界

Visoraft 默认媒体 Worker 不安装 Linux 发行版的 `ffmpeg` 包，也不复制第三方预编译二进制。镜像从 FFmpeg 官方签名源码构建，并在构建失败时直接停止。

## 固定输入

| 项目 | 固定值 |
|---|---|
| FFmpeg | `8.1.2` |
| 源码 | `https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz` |
| 签名 | `https://ffmpeg.org/releases/ffmpeg-8.1.2.tar.xz.asc` |
| 发布密钥 | `FCF986EA15E6E293A5644F10B4322F04D67658D8` |
| OpenH264 | `2.6.0` / `652bdb7719f30b52b08e506645a7322ff1b2cc6f` |
| OpenH264 源码 SHA-256 | `558544AD358283A7AB2930D69A9CEDDF913F4A51EE9BF1BFB9E377322AF81A69` |
| 构建/运行 ABI | Debian Bookworm glibc |

FFmpeg 官方[下载页](https://ffmpeg.org/download.html)公布版本、签名验证流程和发布密钥指纹；[许可页](https://ffmpeg.org/legal.html)明确要求 LGPL 边界不启用 `--enable-gpl` 和 `--enable-nonfree`。

## 构建配置

```text
--prefix=/opt/ffmpeg
--disable-autodetect
--disable-debug
--disable-doc
--disable-ffplay
--disable-network
--disable-static
--enable-libass
--enable-libopenh264
--enable-pic
--enable-shared
--extra-cflags=-I/opt/openh264/include
--extra-ldflags=-L/opt/openh264/lib -Wl,-rpath,/opt/ffmpeg/lib -Wl,-rpath,/opt/openh264/lib
```

边界说明：

- 不传 `--enable-gpl`、`--enable-nonfree` 或 `--enable-version3`。
- 不加入 `libx264`、`libx265`、FDK AAC 等 GPL/nonfree 编码库；只显式加入下述固定版本 OpenH264 与动态链接 libass。
- `--disable-autodetect` 防止构建环境偶然出现的外部库改变许可或功能集合。
- `--disable-network` 使 FFmpeg/FFprobe 只处理 Worker 已下载到本地的受控文件。
- `--enable-libopenh264` 提供 H.264 编码；OpenH264 从固定标签源码构建为共享库，使用 BSD-2-Clause，未启用 GPL `libx264`/`libx265`。
- `--enable-libass` 提供字幕烧录；运行镜像保留 libass 与 Noto CJK 字体许可材料。
- FFmpeg 自有库采用动态链接；Python Worker 通过子进程调用 `ffmpeg`/`ffprobe`，不静态链接 FFmpeg。

## 构建时门禁

Docker 构建会依次执行：

1. 从 `ffmpeg.org` 下载源码、分离签名和发布公钥。
2. 把导入密钥的完整指纹与上表固定指纹逐字比较。
3. 使用 GPG 验证源码压缩包签名。
4. 保存源码 SHA-256。
5. 编译并检查 `ffmpeg -buildconf` 不含 GPL、nonfree 或 version3 开关。
6. 检查 `ffmpeg -L` 明确包含 GNU Lesser General Public License。
7. 检查编码器清单包含 `libopenh264`，且不包含 `libx264`/`libx265`。
8. 执行一帧 OpenH264 实际编码和一段 libass 字幕烧录冒烟测试。
9. 在最终非 root Worker 镜像中再次执行 `ffmpeg -version` 与 `ffprobe -version`。

任一步失败都会使镜像构建失败。

## 对应源码与构建证据

最终 Worker 镜像保留：

```text
/usr/share/ffmpeg-compliance/
├── COPYING.LGPLv2.1
├── build/
│   ├── buildconf.txt
│   ├── ffprobe-version.txt
│   ├── license.txt
│   └── provenance.txt
└── source/
    ├── ffmpeg-8.1.2.tar.xz
    ├── ffmpeg-8.1.2.tar.xz.asc
    ├── ffmpeg-8.1.2.tar.xz.sha256
    └── ffmpeg-devel.asc

/usr/share/openh264-compliance/
├── LICENSE
├── build/provenance.txt
└── source/openh264-2.6.0.tar.gz
```

运行时核验：

```powershell
docker compose exec -T media-worker ffmpeg -hide_banner -version
docker compose exec -T media-worker ffmpeg -hide_banner -buildconf
docker compose exec -T media-worker ffprobe -hide_banner -version
docker compose exec -T transcode-worker ffmpeg -hide_banner -encoders
docker compose exec -T transcode-worker ffmpeg -hide_banner -filters
docker compose exec -T media-worker sh -lc "cd /usr/share/ffmpeg-compliance/source && sha256sum -c ffmpeg-8.1.2.tar.xz.sha256"
```

本文件记录技术边界，不替代针对发行地区、编解码专利和产品分发方式的专业法律评估。
