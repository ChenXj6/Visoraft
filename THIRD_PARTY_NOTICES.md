# Third-party notices

Visoraft's own source code is licensed under Apache-2.0. The following software
is used, built, or orchestrated by the current local stack and remains subject
to its own license terms.

| Component | Current boundary | License |
|---|---|---|
| Go | Build/runtime toolchain | BSD-style Go license |
| pgx | Go PostgreSQL driver | MIT |
| amqp091-go | Go AMQP client | BSD-2-Clause |
| React / React DOM | Web runtime | MIT |
| TanStack Query | Web runtime | MIT |
| React Router | Web runtime | MIT |
| React Hook Form | Web runtime | MIT |
| Zod | Web runtime | MIT |
| Vite and plugin-react | Web build | MIT |
| TypeScript | Web build | Apache-2.0 |
| Python | Media Worker runtime | Python-2.0 |
| pika | Python AMQP client | BSD-3-Clause |
| yt-dlp PyPI wheel | Metadata/download Worker | Unlicense |
| yt-dlp-ejs 0.8.0 | YouTube JavaScript challenge solver scripts | Unlicense AND MIT AND ISC |
| Deno 2.8.3 PyPI wheel | Restricted JavaScript runtime for yt-dlp-ejs | MIT |
| FFmpeg / FFprobe 8.1.2 | Official signed source, locally built shared libraries and CLI tools | LGPL-2.1-or-later |
| OpenH264 2.6.0 | Source-built shared H.264 encoder used by FFmpeg | BSD-2-Clause |
| libass | Dynamically linked subtitle rendering filter | ISC |
| Noto CJK fonts | Runtime glyphs for burned-in CJK subtitles | OFL-1.1 |
| Tesseract OCR 5 | Controlled local OCR for hardcoded subtitle detection | Apache-2.0 |
| Tesseract Chinese language data | Simplified/traditional Chinese OCR models | Apache-2.0 |
| Leptonica | Image processing dependency used by Tesseract | BSD-2-Clause |
| PostgreSQL | Database service | PostgreSQL License |
| RabbitMQ | Queue service | MPL-2.0 |
| SeaweedFS | Local S3-compatible service | Apache-2.0 |
| nginx | Web container | BSD-2-Clause |
| Alpine Linux | Container runtime packages | Mixed; package-specific |

Important distribution boundaries:

- The yt-dlp PyInstaller executables are not included. Official yt-dlp
  documentation states that those bundles include GPLv3+ code.
- The Worker installs yt-dlp's `default` and `pin-deno` dependency groups.
  This fixes yt-dlp, yt-dlp-ejs and Deno to versions tested together by the
  selected yt-dlp release; remote EJS component downloads are not enabled.
- FFmpeg is built from the official PGP-signed 8.1.2 source. The build disables
  autodetection and networking, uses shared libraries, and does not enable GPL,
  nonfree or version3. H.264 encoding uses source-built OpenH264 2.6.0 rather
  than GPL libx264; subtitle burning uses dynamically linked libass. Exact
  FFmpeg and OpenH264 source, checksums, licenses and build evidence are retained
  under `/usr/share/ffmpeg-compliance/` and
  `/usr/share/openh264-compliance/` in the Worker image. See
`docs/v1/architecture/ffmpeg-lgpl-build.md`.
- H.264 may be subject to patent licensing requirements in some jurisdictions.
  Open-source copyright licenses do not by themselves grant every patent right;
  production operators must assess the markets where they encode or distribute.
- Existing hardcoded subtitle detection runs the distribution-packaged Tesseract
  command with simplified and traditional Chinese data. Package copyright files
  are retained under `/usr/share/media-compliance/` in the Worker image.
- MinIO is not included in the default Compose stack. The application remains
  compatible with standard S3 endpoints supplied by an operator.

This hand-maintained file is an initial inventory, not the final release SBOM.
Release automation must generate dependency manifests and verify every
transitive package and container digest.
