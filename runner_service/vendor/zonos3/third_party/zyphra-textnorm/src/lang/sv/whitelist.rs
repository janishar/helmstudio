//! Swedish whitelist classifier.

crate::tsv!(WHITELIST, "sv/whitelist.tsv");

/// Find prefix matches in `text` starting at `start`. Returns `(spoken, end_pos)`.
pub fn prefix_matches<'a>(text: &'a [char], start: usize) -> impl Iterator<Item = (&'a str, usize)> + 'a {
    WHITELIST.prefix_matches(text, start).map(|(r, end)| (r.value.as_str(), end))
}
