# Changelog

## v0.0.2 — 2026-10-18

- Display SHA-256 CA fingerprints and Windows SHA-1 Thumbprints in Connection settings and the setup guide, with instructions for checking downloaded and installed certificates.
- Download and import the complete saved sites, groups and media exceptions as JSON. Imports validate and save atomically, preserve matching statistics and protect unsaved drafts.
- Show live site timelines in Overview, including grayscale, blur, used time and granted or possible bonus time. Reuse DOM elements and defer closed group details to keep refreshes efficient.
- Add regression coverage for certificate identity, configuration transfer, timeline data and browser refresh behavior.

Upgrade by replacing the application binary and restarting it, then reload open pages. Existing configuration, CA and saved site rules remain compatible. Restarting clears in-memory statistics and sessions.
