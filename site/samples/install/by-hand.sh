curl -fsSLO https://github.com/janishar/helmstudio/releases/download/v1.0.0-rc.2/helm_1.0.0-rc.2_darwin_arm64.tar.gz
curl -fsSLO https://github.com/janishar/helmstudio/releases/download/v1.0.0-rc.2/SHA256SUMS
grep ' helm_1.0.0-rc.2_darwin_arm64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
tar -xzf helm_1.0.0-rc.2_darwin_arm64.tar.gz
gh attestation verify helm_1.0.0-rc.2_darwin_arm64.tar.gz --repo janishar/helmstudio
