module github.com/ashfaqc137/ownci

go 1.27.2

// The public site (Node/Astro) lives in website/; keep it out of ./... patterns.
ignore ./website

require (
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
)

require github.com/inconshreveable/mousetrap v1.1.0 // indirect
