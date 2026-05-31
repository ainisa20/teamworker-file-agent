#!/usr/bin/env python3
"""Expose agent ACP stdio via TCP socket for remote code execution."""
import sys
import socket
import subprocess
import threading
import argparse
import os
import time
import shutil

RUNNERS = {
    "opencode": ["opencode", "acp"],
    "claude_code": ["claude"],
    "codex": ["codex"],
    "qwen_code": ["qwen"],
}


def resolve_runner(runner_name):
    """Resolve runner name to command list, checking PATH availability."""
    cmd = RUNNERS.get(runner_name)
    if cmd:
        if shutil.which(cmd[0]):
            return cmd
        return None
    # Fallback: treat as direct command
    if shutil.which(runner_name):
        return [runner_name]
    return None


def bridge_one_client(conn, addr, runner_cmd):
    print(f"[bridge] Client connected: {addr}", flush=True)

    cwd = None
    leftover = b""
    conn.settimeout(10)
    try:
        first_chunk = conn.recv(65536)
    except socket.timeout:
        first_chunk = b""

    if first_chunk.startswith(b"CWD:"):
        newline_pos = first_chunk.find(b"\n")
        if newline_pos != -1:
            cwd = first_chunk[4:newline_pos].decode("utf-8", errors="replace").strip()
            leftover = first_chunk[newline_pos + 1:]
            print(f"[bridge] CWD: {cwd}", flush=True)
        else:
            leftover = first_chunk
    else:
        leftover = first_chunk

    cmd = list(runner_cmd)
    if cwd and runner_cmd[-1] == "acp":
        cmd.extend(["--cwd", cwd])

    proc = subprocess.Popen(
        cmd,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        cwd=cwd,
    )
    print(f"[bridge] {' '.join(cmd)} started (PID: {proc.pid})", flush=True)

    if leftover:
        proc.stdin.write(leftover)
        proc.stdin.flush()

    stopped = threading.Event()

    def stdout_to_tcp():
        try:
            while not stopped.is_set():
                data = proc.stdout.read1(65536)
                if not data:
                    break
                conn.sendall(data)
                try:
                    conn.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
                except OSError:
                    pass
                print(f"[bridge] Sent {len(data)} bytes to client", flush=True)
        except (BrokenPipeError, ConnectionResetError, OSError):
            pass
        finally:
            stopped.set()

    t = threading.Thread(target=stdout_to_tcp, daemon=True)
    t.start()

    try:
        while not stopped.is_set():
            conn.settimeout(1.0)
            try:
                data = conn.recv(65536)
                if not data:
                    break
                proc.stdin.write(data)
                proc.stdin.flush()
            except socket.timeout:
                continue
            except (ConnectionResetError, OSError):
                break
    except Exception:
        pass
    finally:
        stopped.set()
        try:
            proc.stdin.close()
        except Exception:
            pass
        t.join(timeout=10)

        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()

        # Wait for tunnel to flush all data before closing
        time.sleep(2)

        try:
            conn.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        conn.close()
        print(f"[bridge] Session ended (exit: {proc.returncode})", flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=4096)
    parser.add_argument("--hostname", default="127.0.0.1")
    parser.add_argument("--runner", default="opencode")
    args = parser.parse_args()

    runner_cmd = resolve_runner(args.runner)
    if not runner_cmd:
        print(f"[bridge] Runner '{args.runner}' not found in PATH", flush=True)
        sys.exit(1)

    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind((args.hostname, args.port))
    server.listen(5)
    print(f"[bridge] Listening on {args.hostname}:{args.port} (runner: {args.runner})", flush=True)
    print(f"[bridge] Waiting for connections... (Ctrl+C to stop)", flush=True)

    try:
        while True:
            server.settimeout(1.0)
            try:
                conn, addr = server.accept()
            except socket.timeout:
                continue
            bridge_one_client(conn, addr, runner_cmd)
    except KeyboardInterrupt:
        print("\n[bridge] Shutting down", flush=True)
    finally:
        server.close()


if __name__ == "__main__":
    main()
