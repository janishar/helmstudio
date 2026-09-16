# Record with provenance

A studio's outputs belong in helmstudio's library, with what made them. Two calls do it: **adopt** the file the studio wrote, then **record** a gallery item that holds it, with the parameters that made it and the inputs it was made from. Every item made from another item can then be traced both ways.

This studio holds `assets` and `gallery`:

@sample record/record.py

## Adopt, don't upload

A studio writes its output to `HELM_STAGE_DIR`, a directory helmstudio gives each run, and adopts it from there. Adopting hardlinks the file into the asset store: no bytes are copied and nothing is sent over HTTP, so adopting a long video costs no more than adopting a small image. A file on a different volume from the store cannot be hardlinked, and adopting it is refused rather than quietly copied.

A studio can also adopt from its own data directory, `{data}`. A file left in the stage directory unadopted is removed when the studio's processes stop.

Assets are deduplicated by content: adopting the same bytes twice gives back the same asset.

## Record the item

An item names its asset, its `kind`, and `params` — whatever made it, in the studio's own terms. `inputs` is the provenance: each input names an asset and the `role` it played, such as a reference image or a first frame. The role is the studio's word; helmstudio keeps it and shows it.

## Walk the lineage

- `gallery.lineage(item)` goes **upstream**: the items whose outputs this one was made from, recursively.
- `assets.lineage(asset)` goes **downstream**: every item made from that asset.

Both leave out items another studio made unless the caller holds `gallery.read_all`, whose sentence on the approval screen is a warning for exactly that reason. See [capabilities](/docs/concepts/capabilities/).
