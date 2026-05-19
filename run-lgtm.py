#!/usr/bin/env python3
"""Run the Grafana otel-lgtm container with the sandbox's mounts and env."""

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).parent.resolve()
LOCAL_VOLUME = REPO / "container"
ENV_FILE = REPO / ".env"


def detect_runtime() -> tuple[str, str]:
    if shutil.which("podman"):
        # Fedora's default SELinux config requires the "z" option for bind mounts.
        # https://docs.podman.io/en/stable/markdown/podman-run.1.html (Labeling Volume Mounts)
        return "podman", "rw,z"
    if shutil.which("docker"):
        return "docker", "rw"
    sys.exit("Neither podman nor docker found.")


def obi_flags() -> tuple[list[str], list[str]]:
    if os.environ.get("ENABLE_OBI") != "true":
        return [], []
    container_flags = ["--pid=host", "--privileged"]
    env_flags = ["-e", "ENABLE_OBI=true"]
    for var, val in os.environ.items():
        if var.startswith(("OBI_TARGET", "OTEL_EBPF_", "ENABLE_LOGS_OBI")):
            env_flags += ["-e", f"{var}={val}"]
    return container_flags, env_flags


def mount(runtime_opts: str, src: Path, dst: str) -> list[str]:
    return ["-v", f"{src}:{dst}:{runtime_opts}"]


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("release", nargs="?", default="latest",
                   help="otel-lgtm image tag (default: latest)")
    p.add_argument("--use-local-image", action="store_true",
                   help="use a locally-built grafana/otel-lgtm:latest image")
    args = p.parse_args()

    for sub in ("grafana", "prometheus", "loki"):
        (LOCAL_VOLUME / sub).mkdir(parents=True, exist_ok=True)
    ENV_FILE.touch(exist_ok=True)

    runtime, mount_opts = detect_runtime()
    obi_container_flags, obi_env_flags = obi_flags()
    if obi_container_flags:
        print("OBI eBPF auto-instrumentation enabled. Adding --pid=host --privileged flags.")

    tty_flags = ["-t", "-i"] if sys.stdin.isatty() else []

    if args.use_local_image:
        image = "localhost/grafana/otel-lgtm:latest" if runtime == "podman" else "grafana/otel-lgtm:latest"
    else:
        image = f"docker.io/grafana/otel-lgtm:{args.release}"
        subprocess.run([runtime, "image", "pull", image], check=True)

    cmd = [
        runtime, "container", "run",
        "--name", "lgtm",
        *obi_container_flags,
        *obi_env_flags,
        "-p", "3000:3000",
        "-p", "4040:4040",
        "-p", "4317:4317",
        "-p", "4318:4318",
        "-p", "9090:9090",
        "--rm",
        *tty_flags,
        *mount(mount_opts, LOCAL_VOLUME / "grafana", "/data/grafana"),
        *mount(mount_opts, LOCAL_VOLUME / "prometheus", "/data/prometheus"),
        *mount(mount_opts, LOCAL_VOLUME / "loki", "/data/loki"),
        *mount(mount_opts, REPO / "grafana" / "provisioning", "/etc/grafana/provisioning"),
        *mount(mount_opts, REPO / "grafana" / "dashboards", "/var/lib/grafana/dashboards"),
        "-e", "GF_PATHS_DATA=/data/grafana",
        "--env-file", str(ENV_FILE),
        image,
    ]

    # Replace this process so Ctrl-C and signals go straight to the container.
    os.execvp(cmd[0], cmd)


if __name__ == "__main__":
    main()
