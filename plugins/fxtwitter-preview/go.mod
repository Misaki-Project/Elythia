module github.com/Misaki-Project/Elythia/plugins/fxtwitter-preview

go 1.27.3

require (
	github.com/blezek/tga v0.0.0-20150626111426-80720cbc1017
	github.com/jackc/pgx/v5 v5.9.2
	github.com/kovidgoyal/imaging v1.8.21
	github.com/elythia-network/elythia v0.0.0
	golang.org/x/image v0.45.0
	golang.org/x/sync v0.24.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kovidgoyal/go-parallel v1.1.1 // indirect
	github.com/kovidgoyal/go-shm v1.0.0 // indirect
	github.com/rwcarlsen/goexif v0.0.0-20190401172101-9e8deecbddbd // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/elythia-network/elythia => ../..
