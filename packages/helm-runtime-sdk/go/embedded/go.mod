module github.com/janishar/helmstudio/packages/helm-runtime-sdk/go/embedded

go 1.27.1

require (
	github.com/janishar/helmstudio v0.4.0
	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go v0.4.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1 // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.58.0 // indirect
)

replace (
	github.com/janishar/helmstudio => ../../../..
	github.com/janishar/helmstudio/packages/helm-runtime-sdk/go => ..
)
