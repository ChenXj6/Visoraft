from __future__ import annotations

from collections.abc import Callable
from pathlib import Path
from typing import Any

import boto3
from boto3.s3.transfer import TransferConfig
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError

from .settings import Settings


class StorageFailure(RuntimeError):
    """Raised when an S3 operation did not complete."""


class ObjectStorage:
    def __init__(self, settings: Settings) -> None:
        self.bucket = settings.s3_bucket
        self._client = boto3.client(
            "s3",
            endpoint_url=settings.s3_endpoint,
            aws_access_key_id=settings.s3_access_key,
            aws_secret_access_key=settings.s3_secret_key,
            region_name=settings.s3_region,
            config=Config(
                signature_version="s3v4",
                retries={"max_attempts": 3, "mode": "standard"},
                s3={"addressing_style": "path"},
                connect_timeout=5,
                read_timeout=60,
            ),
        )
        self._transfer = TransferConfig(
            multipart_threshold=16 * 1024 * 1024,
            multipart_chunksize=16 * 1024 * 1024,
            max_concurrency=2,
            use_threads=True,
        )

    def ensure_bucket(self) -> None:
        try:
            self._client.head_bucket(Bucket=self.bucket)
            return
        except ClientError as exc:
            code = str(exc.response.get("Error", {}).get("Code", ""))
            status = int(exc.response.get("ResponseMetadata", {}).get("HTTPStatusCode", 0))
            if code not in {"404", "NoSuchBucket", "NotFound"} and status != 404:
                raise StorageFailure(f"could not inspect S3 bucket {self.bucket}: {code}") from exc
        except BotoCoreError as exc:
            raise StorageFailure(f"could not connect to S3: {exc}") from exc

        try:
            self._client.create_bucket(Bucket=self.bucket)
        except ClientError as exc:
            code = str(exc.response.get("Error", {}).get("Code", ""))
            if code not in {"BucketAlreadyExists", "BucketAlreadyOwnedByYou"}:
                raise StorageFailure(f"could not create S3 bucket {self.bucket}: {code}") from exc
        except BotoCoreError as exc:
            raise StorageFailure(f"could not create S3 bucket {self.bucket}: {exc}") from exc

    def upload_file(
        self,
        path: Path,
        object_key: str,
        content_type: str,
        metadata: dict[str, str],
        on_progress: Callable[[int], None] | None = None,
    ) -> None:
        transferred = 0

        def callback(byte_count: int) -> None:
            nonlocal transferred
            transferred += byte_count
            if on_progress is not None:
                on_progress(transferred)

        extra_args: dict[str, Any] = {
            "ContentType": content_type,
            "Metadata": metadata,
        }
        try:
            self._client.upload_file(
                str(path),
                self.bucket,
                object_key,
                ExtraArgs=extra_args,
                Callback=callback,
                Config=self._transfer,
            )
        except (BotoCoreError, ClientError, OSError) as exc:
            raise StorageFailure(f"could not upload media object: {exc}") from exc

    def download_file(
        self,
        object_key: str,
        destination: Path,
        bucket: str | None = None,
    ) -> None:
        try:
            destination.parent.mkdir(parents=True, exist_ok=True)
            self._client.download_file(
                bucket or self.bucket,
                object_key,
                str(destination),
                Config=self._transfer,
            )
        except (BotoCoreError, ClientError, OSError) as exc:
            raise StorageFailure(f"could not download media object: {exc}") from exc

    def download_file_if_exists(
        self,
        object_key: str,
        destination: Path,
        bucket: str | None = None,
    ) -> bool:
        try:
            destination.parent.mkdir(parents=True, exist_ok=True)
            self._client.download_file(
                bucket or self.bucket,
                object_key,
                str(destination),
                Config=self._transfer,
            )
            return True
        except ClientError as exc:
            code = str(exc.response.get("Error", {}).get("Code", ""))
            status = int(exc.response.get("ResponseMetadata", {}).get("HTTPStatusCode", 0))
            if code in {"404", "NoSuchKey", "NotFound"} or status == 404:
                return False
            raise StorageFailure(f"could not download media object: {exc}") from exc
        except (BotoCoreError, OSError) as exc:
            raise StorageFailure(f"could not download media object: {exc}") from exc

    def delete_object(self, object_key: str, bucket: str | None = None) -> None:
        try:
            self._client.delete_object(Bucket=bucket or self.bucket, Key=object_key)
        except (BotoCoreError, ClientError) as exc:
            raise StorageFailure(f"could not delete media object: {exc}") from exc
