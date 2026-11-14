"""The object store (S3 API: SeaweedFS in compose, S3 in the cloud). boto3 is sync, so calls run in a thread."""

import asyncio

import boto3
from botocore.config import Config

import config


class Store:
    """Writes run outputs to the S3_BUCKET bucket."""

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
