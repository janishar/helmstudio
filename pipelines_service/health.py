"""GET /health on HEALTH_PORT (8082): 200 while NATS is connected and the database answers, else 503.
`python health.py` asks it and exits 0 or 1 (the container health check; the slim image has no curl)."""

import asyncio
import contextlib
import sys
import urllib.request

import psycopg
from psycopg_pool import PoolTimeout

import config


async def _answer(reader: asyncio.StreamReader, writer: asyncio.StreamWriter, nc, pool) -> None:
    ok = nc.is_connected
    try:
        await asyncio.wait_for(reader.readline(), 2)
        if ok:
            async with pool.connection(timeout=2) as conn:
                await conn.execute("SELECT 1")
    except OSError, TimeoutError, psycopg.Error, PoolTimeout:
        ok = False
    body = b'{"status":"ok"}' if ok else b'{"status":"unavailable"}'
    writer.write(
        b"HTTP/1.1 %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n"
        b"Connection: close\r\n\r\n%s"
        % (b"200 OK" if ok else b"503 Service Unavailable", len(body), body)
    )
    with contextlib.suppress(Exception):
        await writer.drain()
        writer.close()


async def serve(nc, pool) -> asyncio.Server:
    return await asyncio.start_server(
        lambda r, w: _answer(r, w, nc, pool), "0.0.0.0", config.HEALTH_PORT
    )


def check() -> int:
    """0 when the running service answers 200, else 1."""
    try:
        urllib.request.urlopen(f"http://127.0.0.1:{config.HEALTH_PORT}/health", timeout=3)
    except OSError:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(check())
