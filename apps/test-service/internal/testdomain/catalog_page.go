package testdomain

import "time"

const (
	DefaultCatalogPageSize = 200
	MaxCatalogPageSize     = 1000
)

type CatalogPageRequest struct {
	ProjectID string
	ProfileID string
	Cursor    string
	Limit     int
}

type CatalogPage struct {
	ProjectID   string
	ProfileID   string
	Revision    string
	GeneratedAt time.Time
	Containers  []Container
	Items       []Item
	Diagnostics []Diagnostic
	Partial     bool
	NextCursor  string
}

func (page CatalogPage) Clone() CatalogPage {
	page.Containers = make([]Container, len(page.Containers))
	for index, container := range page.Containers {
		page.Containers[index] = cloneContainer(container)
	}
	page.Items = make([]Item, len(page.Items))
	for index, item := range page.Items {
		page.Items[index] = cloneItem(item)
	}
	page.Diagnostics = append([]Diagnostic(nil), page.Diagnostics...)
	return page
}
