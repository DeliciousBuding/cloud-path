package plugincatalog

// Catalog is the read-only plugin-installation view consumed by the server.
// Instance views are built directly from ProjectionSource and are not part of
// this interface; the write API and instance read API must share that projection.
type Catalog interface {
	Plugins(tenant string) ([]PluginView, error)
	Plugin(tenant, id string) (PluginView, bool, error)
}

// maxCatalogItems bounds one list response; pagination can be added when a real
// caller needs it.
const maxCatalogItems = 1000

// capList truncates a list to the response-size limit.
func capList[T any](in []T) []T {
	if len(in) > maxCatalogItems {
		return in[:maxCatalogItems]
	}
	return in
}

// emptyCatalog is the nil-source result: empty lists / not found, never panic.
type emptyCatalog struct{}

func (emptyCatalog) Plugins(string) ([]PluginView, error) { return []PluginView{}, nil }
func (emptyCatalog) Plugin(_, _ string) (PluginView, bool, error) {
	return PluginView{}, false, nil
}
