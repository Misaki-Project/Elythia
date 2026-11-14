package trackingparametercleaner

import "github.com/elythia-network/elythia/plugin"

// Plugin is a metadata-only host registration. Paste transforms run in the frontend;
// this definition deliberately declares no migrations, jobs, or HTTP routes.
var Plugin = plugin.Definition{
	Name:       "tracking-parameter-cleaner",
	Version:    "1.0.0",
	APIVersion: plugin.APIVersion,
	Routes:     func(plugin.Context, plugin.Router) error { return nil },
}
