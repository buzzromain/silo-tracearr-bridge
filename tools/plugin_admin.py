"""Install, configure, and inspect the plugin through Silo's admin API.

Configuration comes from SILO_URL, SILO_API_KEY, BRIDGE_API_KEY, and
optionally BRIDGE_NAME. Secrets are never printed.
"""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.error import HTTPError
from urllib.request import HTTPRedirectHandler, Request, build_opener
import uuid


class NoRedirects(HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def request_json(base, key, bridge_key, method, path, data=None, content_type=None, headers=None):
    request_headers = {
        "Accept": "application/json",
        "Authorization": "Bearer " + key,
    }
    request_headers.update(headers or {})
    if data is not None:
        request_headers["Content-Type"] = content_type or "application/json"
        if not isinstance(data, bytes):
            data = json.dumps(data).encode()
    try:
        with build_opener(NoRedirects).open(
            Request(base + path, data=data, headers=request_headers, method=method),
            timeout=90,
        ) as response:
            raw = response.read()
            return (json.loads(raw) if raw else {}), response.headers
    except HTTPError as error:
        raw = error.read(8192).decode(errors="replace")
        error.close()
        for secret in (key, bridge_key):
            if secret:
                raw = raw.replace(secret, "[REDACTED]")
        raise RuntimeError(f"Silo HTTP {error.code}: {raw}") from None


def install(base, key, bridge_key, binary, replace):
    manifest = json.loads(subprocess.check_output([str(binary.resolve()), "manifest"]))
    plugin_id = manifest.get("pluginId", manifest.get("plugin_id"))
    if plugin_id != "community.buzzromain.tracearr-bridge":
        raise RuntimeError("This tool only installs community.buzzromain.tracearr-bridge")

    existing, _ = request_json(base, key, bridge_key, "GET", "/api/v2/admin/plugins/installations")
    if any(item.get("plugin_id") == plugin_id for item in existing.get("items", [])) and not replace:
        raise RuntimeError("Plugin is already installed; use --replace explicitly")

    boundary = "bridge-" + uuid.uuid4().hex
    payload = (
        f'--{boundary}\r\n'
        'Content-Disposition: form-data; name="archive"; '
        'filename="tracearr-bridge-linux-amd64"\r\n'
        "Content-Type: application/octet-stream\r\n\r\n"
    ).encode()
    payload += binary.read_bytes()
    payload += f"\r\n--{boundary}--\r\n".encode()
    result, _ = request_json(
        base,
        key,
        bridge_key,
        "POST",
        "/api/v2/admin/plugins/uploads",
        payload,
        "multipart/form-data; boundary=" + boundary,
    )
    print(json.dumps({field: result.get(field) for field in ("id", "plugin_id", "version", "enabled")}, indent=2))


def configure(base, key, bridge_key, installation_id, native_url, listen):
    value = {
        "silo_url": native_url,
        "silo_api_key": key,
        "bridge_api_key": bridge_key,
        "listen_address": listen,
        "server_name": os.environ.get("BRIDGE_NAME", "Silo (Tracearr bridge)"),
    }
    request_json(
        base,
        key,
        bridge_key,
        "PUT",
        f"/api/v2/admin/plugins/installations/{installation_id}/config",
        {"key": "bridge", "value": value},
    )
    print("Plugin settings saved. Open diagnostics to start and verify the listener.")


def recovery(base, key, bridge_key, installation_id):
    path = f"/api/v2/admin/tasks/plugin:{installation_id}:listener/triggers"
    schedule, response_headers = request_json(base, key, bridge_key, "GET", path)
    etag = next((value for name, value in response_headers.items() if name.lower() == "etag"), None)
    if not etag:
        raise RuntimeError("Silo did not provide a schedule ETag")

    triggers = schedule.get("triggers", [])
    if not any(trigger.get("type") == "startup" for trigger in triggers):
        triggers.append({"type": "startup"})
    if not any(
        trigger.get("type") == "interval" and 0 < trigger.get("interval_ms", 0) <= 60000
        for trigger in triggers
    ):
        triggers.append({"type": "interval", "interval_ms": 60000})
    request_json(
        base,
        key,
        bridge_key,
        "PUT",
        path,
        {"triggers": triggers},
        headers={"If-Match": etag},
    )
    saved, _ = request_json(base, key, bridge_key, "GET", path)
    print(json.dumps(saved, indent=2))


def status(base, key, bridge_key, installation_id):
    result, _ = request_json(
        base,
        key,
        bridge_key,
        "GET",
        f"/api/v2/plugin-content/plugins/{installation_id}/status",
    )
    print(json.dumps(result, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)

    install_parser = commands.add_parser("install")
    install_parser.add_argument("binary", type=Path)
    install_parser.add_argument("--replace", action="store_true")

    configure_parser = commands.add_parser("configure")
    configure_parser.add_argument("id", type=int)
    configure_parser.add_argument("--native-url", default="http://127.0.0.1:8080")
    configure_parser.add_argument("--listen", default="0.0.0.0:8098")

    status_parser = commands.add_parser("status")
    status_parser.add_argument("id", type=int)

    recovery_parser = commands.add_parser("recovery")
    recovery_parser.add_argument("id", type=int)

    args = parser.parse_args()
    base = os.environ["SILO_URL"].rstrip("/")
    key = os.environ["SILO_API_KEY"]
    bridge_key = os.environ.get("BRIDGE_API_KEY", "")

    if args.command == "install":
        install(base, key, bridge_key, args.binary, args.replace)
    elif args.command == "configure":
        configure(base, key, bridge_key, args.id, args.native_url, args.listen)
    elif args.command == "recovery":
        recovery(base, key, bridge_key, args.id)
    else:
        status(base, key, bridge_key, args.id)


if __name__ == "__main__":
    try:
        main()
    except (KeyError, RuntimeError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
