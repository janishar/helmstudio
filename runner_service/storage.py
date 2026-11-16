"""The object store (S3 API: SeaweedFS in compose, S3 in the cloud). boto3 is sync, so calls run in a thread."""

import asyncio
import hashlib

import boto3
from botocore.config import Config
from botocore.exceptions import ClientError

import config


class Store:
    """Writes run outputs to and reads input images from the S3_BUCKET bucket."""

    def __init__(self) -> None:
        self.s3 = boto3.client(
            "s3",
            region_name=config.S3_REGION,
            endpoint_url=config.S3_ENDPOINT or None,
            aws_access_key_id=config.S3_ACCESS_KEY_ID or None,
            aws_secret_access_key=config.S3_SECRET_ACCESS_KEY or None,
            # ponytail: path-style always (SeaweedFS needs it; AWS still serves it)
            config=Config(
                signature_version="s3v4",
                retries={"max_attempts": 3, "mode": "standard"},
                s3={"addressing_style": "path"},
            ),
        )

    async def put(self, key: str, data: bytes, content_type: str) -> None:
        """Upload `data` to `key`."""
        await asyncio.to_thread(
            self.s3.put_object,
            Bucket=config.S3_BUCKET,
            Key=key,
            Body=data,
            ContentType=content_type,
        )

    async def upload(self, key: str, path, content_type: str) -> tuple[int, str]:
        """Stream the file at `path` to `key` from disk (multipart past 1 GiB, so it is never held in memory and may
        pass S3's 5 GB single-PUT cap): (bytes, the stored object's ETag)."""
        from boto3.s3.transfer import TransferConfig

        def send() -> tuple[int, str]:
            self.s3.upload_file(str(path), config.S3_BUCKET, key, ExtraArgs={"ContentType": content_type},
                                Config=TransferConfig(multipart_threshold=1 << 30, multipart_chunksize=64 << 20))  # fmt: skip
            head = self.s3.head_object(Bucket=config.S3_BUCKET, Key=key)
            return head["ContentLength"], head["ETag"].strip('"')

        return await asyncio.to_thread(send)

    async def get(self, key: str, limit: int, etag: str | None = None) -> bytes | None:
        """The object at `key` (at most limit + 1 bytes, so a larger one shows), or None when it is missing or, with
        `etag`, its ETag is another (the object was replaced)."""

        def read() -> bytes | None:
            match = {"IfMatch": f'"{etag}"'} if etag else {}
            try:
                r = self.s3.get_object(Bucket=config.S3_BUCKET, Key=key, **match)
            except ClientError as e:
                code = e.response.get("Error", {}).get("Code")
                if code in ("NoSuchKey", "404", "PreconditionFailed", "412"):
                    return None
                raise
            with r["Body"] as body:
                return body.read(limit + 1)

        return await asyncio.to_thread(read)

    async def download(self, key: str, dest, limit: int, stop=None) -> tuple[int, str] | None:
        """Stream the object at `key` into the file `dest` (at most limit + 1 bytes): (bytes, SHA-256 hex), or None
        when it is missing; `stop()` true ends it early (the caller checks why)."""

        def read() -> tuple[int, str] | None:
            try:
                r = self.s3.get_object(Bucket=config.S3_BUCKET, Key=key)
            except ClientError as e:
                if e.response.get("Error", {}).get("Code") in ("NoSuchKey", "404"):
                    return None
                raise
            h, n = hashlib.sha256(), 0
            with r["Body"] as body, open(dest, "wb") as f:
                while (
                    n <= limit
                    and not (stop and stop())
                    and (chunk := body.read(min(1 << 22, limit + 1 - n)))
                ):
                    h.update(chunk)
                    f.write(chunk)
                    n += len(chunk)
            return n, h.hexdigest()

        return await asyncio.to_thread(read)
