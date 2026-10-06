#!/usr/bin/env python3
"""Start a built native binary with isolated state; Linux also checks SIGTERM."""
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.request

binary = str(Path(sys.argv[1]).resolve())
root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="pauseproxy-smoke-") as directory:
    directory = Path(directory)
    sockets = [socket.socket() for _ in range(3)]
    for sock in sockets:
        sock.bind(("127.0.0.1", 0))
    ports = [sock.getsockname()[1] for sock in sockets]
    config = json.loads((root / "config.dev.json").read_text())
    for key, port in zip(("proxy_addr", "pac_addr", "admin_addr"), ports):
        config[key] = f"127.0.0.1:{port}"
    config.update(advertised_proxy=config["proxy_addr"],
                  admin_url=f"https://127.0.0.1:{ports[2]}",
                  admin_password=secrets.token_urlsafe(24),
                  ca_dir=str(directory / "ca"), sites_file=str(directory / "sites.json"))
    path = directory / "config.json"
    path.write_text(json.dumps(config))
    subprocess.run([binary, "bootstrap-ca", "--config", str(path)], check=True, capture_output=True)
    key = directory / "ca/ca-key.pem"
    original = hashlib.sha256(key.read_bytes()).digest()
    again = subprocess.run([binary, "bootstrap-ca", "--config", str(path)], capture_output=True)
    assert again.returncode != 0 and hashlib.sha256(key.read_bytes()).digest() == original
    for sock in sockets:
        sock.close()
    context = ssl.create_default_context(cafile=str(directory / "ca/ca-cert.pem"))
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                         urllib.request.HTTPSHandler(context=context))
    with subprocess.Popen([binary, "--config", str(path)], stdout=subprocess.PIPE, stderr=subprocess.PIPE) as process:
        try:
            for attempt in range(100):
                if process.poll() is not None:
                    raise AssertionError(process.communicate())
                try:
                    with opener.open(f"http://127.0.0.1:{ports[1]}/healthz", timeout=1) as response:
                        assert response.status == 200
                    break
                except OSError:
                    time.sleep(0.1)
            else:
                raise AssertionError("Server did not become ready")
            for url in (f"http://127.0.0.1:{ports[1]}/proxy.pac",
                        f"http://127.0.0.1:{ports[1]}/demo", config["admin_url"] + "/howto"):
                with opener.open(url, timeout=3) as response:
                    assert response.status == 200 and response.read()
            process.terminate()
            result = process.wait(timeout=8)
            if os.name != "nt":
                assert result == 0, f"SIGTERM did not shut down cleanly: {result}"
            assert hashlib.sha256(key.read_bytes()).digest() == original
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
print("PASS: isolated local start, health, PAC, demo, HTTPS admin, CA preservation" +
      (", graceful SIGTERM" if os.name != "nt" else ""))
