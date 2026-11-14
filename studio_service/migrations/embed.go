// Package migrations: goose SQL files (00001 platform: outbox, projector dedupe; 00002 Catalog Agent tables; 00003
// the deferred re-check flag; 00004 per-image attempts, first_pass and
// render-time product for the quality metrics; 00005 every render try of an image; 00006
// garments' kurti and kurta-pant ids moved to tops and sets; 00007 jewellery's style subcategories moved to the
// product list's types; 00008 stopped images; 00009 furniture's style subcategories moved to its types).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
