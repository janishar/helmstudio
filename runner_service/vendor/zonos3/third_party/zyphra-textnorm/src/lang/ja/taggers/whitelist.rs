//! `ja/taggers/whitelist.py`: `data/whitelist.tsv` (`string_file`), no `convert_space` (so `Ph.D.` → `p h d`
//! carries plain spaces into the tag, which no verbalizer accepts).

use super::Sink;
use crate::text::Cost;

crate::tsv!(WHITELIST, "ja/whitelist.tsv");

pub struct WhiteList {
    pub cost: Cost,
}

impl WhiteList {
    /// Whether a key starts with `c`.
    pub fn can_start(c: char) -> bool {
        WHITELIST.rows().iter().any(|r| r.key.starts_with(c))
    }

    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        // every key starts with an ASCII char or a Greek letter
        if (t[start] as u32) < 0x21 || t[start] > 'ω' {
            return;
        }
        for (row, end) in WHITELIST.prefix_matches(t, start) {
            if end > start {
                out.push(end, self.cost + row.cost, 0, |tag| {
                    tag.push_str("name: \"");
                    tag.push_str(&row.value);
                    tag.push('"');
                });
            }
        }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn keys_start_below_omega() {
        for row in super::WHITELIST.rows() {
            let c = row.key.chars().next().unwrap();
            assert!(c as u32 >= 0x21 && c <= 'ω', "{c:?}");
        }
    }
}
