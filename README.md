# YouTube Video Downloader

I vibe coded this with [T3 Chat](https://t3.chat/), and it's a basic tool to download YouTube videos at 1080p (if video is more than 1080p it'll still download at 1080p, if max quality of video is lower than 1080p it'll download at maximum quality of the video) there isn't any quality option in this tool.

All you need to do is run this app via Docker Desktop on your machine using `docker compose` or `docker` commands and then download any YouTube video from your browser by going into this url:

[http://localhost:7839/download?url=<url>](http://localhost:7839/download?url=https://www.youtube.com/watch?v=rQ7tMWOCQlM) just replace the URL.

All endpoints are:

- /download
- /health
- /metrics
- /config

Other endpoints are useless so I won't talk about them.

My main goal with this tool was to download YouTube videos without silly ads or wasting time at broken download tools on the internet.

---