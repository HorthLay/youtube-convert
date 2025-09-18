# 🎥 YouTube Converter in Golang (FFmpeg)

A simple YouTube video converter built with **Go** and **FFmpeg**.  
This tool allows you to download videos from YouTube and convert them into audio (MP3) or video formats.

---

## ✨ Features

- ✅ Download YouTube videos using [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) or [`youtube-dl`](https://github.com/ytdl-org/youtube-dl)
- ✅ Convert videos to MP3, MP4, or other formats via **FFmpeg**
- ✅ Support for custom output names
- ✅ Cross-platform (Windows, macOS, Linux)
- ✅ CLI-based — lightweight and fast

---

## 📦 Requirements

Before running, ensure you have the following installed:

- [Go](https://go.dev/dl/) **1.18+**
- [FFmpeg](https://ffmpeg.org/download.html)
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) or `youtube-dl`

**Example installation (Ubuntu/Debian):**
```bash
sudo apt update
sudo apt install ffmpeg python3-pip -y
pip install yt-dlp
