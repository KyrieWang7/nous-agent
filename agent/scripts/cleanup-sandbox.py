#!/usr/bin/env python3
"""Clean up orphan sandbox containers from previous LangGraph process."""

import os
import sys

def main():
    try:
        import docker
    except ImportError:
        print("docker SDK not available, skipping sandbox cleanup")
        return

    prefix = os.getenv("SANDBOX_CONTAINER_PREFIX", "nous-ai-sandbox")
    try:
        client = docker.from_env()
        containers = client.containers.list(filters={"name": prefix})
        if not containers:
            print(f"No orphan sandbox containers found (prefix={prefix})")
            return
        for ct in containers:
            try:
                ct.stop(timeout=5)
                print(f"  Stopped orphan: {ct.name}")
            except Exception as e:
                print(f"  Failed to stop {ct.name}: {e}", file=sys.stderr)
        print(f"Cleaned up {len(containers)} orphan sandbox container(s)")
    except Exception as e:
        print(f"Sandbox cleanup skipped: {e}", file=sys.stderr)


if __name__ == "__main__":
    main()
