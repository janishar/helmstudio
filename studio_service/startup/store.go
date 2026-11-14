package startup

import (
	"context"
	"fmt"

	"github.com/helmstudio/helmgo/storage"

	"github.com/helmstudio/studio_service/config"
)

// objectStore builds the object store STORAGE_DRIVER names (the same store files_service serves).
func objectStore(ctx context.Context, env config.Env) (storage.Store, error) {
	switch env.StorageDriver {
	case "memory":
		return storage.NewMemory(), nil
	case "s3":
		return storage.NewS3(ctx, storage.S3Config{Bucket: env.S3Bucket, Region: env.S3Region, Endpoint: env.S3Endpoint,
			AccessKeyID: env.S3AccessKey, SecretAccessKey: env.S3SecretKey, ForcePathStyle: env.S3PathStyle})
	}
	return nil, fmt.Errorf("STORAGE_DRIVER %q: use s3 or memory", env.StorageDriver)
}
