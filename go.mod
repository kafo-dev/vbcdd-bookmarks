module vbcdd.kafo.dev/bookmarks

go 1.27.1

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c
	golang.org/x/net v0.61.0
)

require (
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	honnef.co/go/tools v0.8.1 // indirect
	mvdan.cc/gofumpt v0.12.0 // indirect
)

tool (
	honnef.co/go/tools/cmd/staticcheck
	mvdan.cc/gofumpt
)
