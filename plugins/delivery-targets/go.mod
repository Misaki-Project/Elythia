module github.com/Misaki-Project/Elythia/plugins/delivery-targets

go 1.27.4

require (
	github.com/elythia-network/elythia v0.0.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/stretchr/testify v1.12.1
	golang.org/x/net v0.62.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/elythia-network/elythia => ../..
