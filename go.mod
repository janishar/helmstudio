module github.com/janishar/helmstudio

go 1.27.1

require (
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.58.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

require github.com/janishar/helmstudio/packages/helm-runtime-sdk/go v0.4.0

replace github.com/janishar/helmstudio/packages/helm-runtime-sdk/go => ./packages/helm-runtime-sdk/go
