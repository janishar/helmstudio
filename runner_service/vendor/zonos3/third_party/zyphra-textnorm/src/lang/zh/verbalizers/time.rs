//! `zh/verbalizers/time.py`: `graph_regular` (`H | M | S | H M S | H M | H S | M S`) | `graph_back_count`
//! (`[V →"差 "] H [V] (M | S | M S)`), optionally followed by `suffix: "<(AM|PM)*>"`. Values pass through.

use std::sync::LazyLock;

use super::Cur;
use crate::text::Table;

crate::tsv!(AM, "zh/time/AM.tsv");
crate::tsv!(PM, "zh/time/PM.tsv");

#[derive(Clone, Copy, PartialEq)]
enum K {
    H,
    M,
    S,
    V,
    Suffix,
}

const KEYS: [(&str, K); 5] = [
    ("hours: \"", K::H),
    ("minutes: \"", K::M),
    ("seconds: \"", K::S),
    ("morphosyntactic_features: \"", K::V),
    ("suffix: \"", K::Suffix),
];

static SUFFIXES: LazyLock<Vec<Vec<char>>> = LazyLock::new(|| {
    let tables: [&Table; 2] = [&AM, &PM];
    tables.iter().flat_map(|t| t.rows()).map(|r| r.key.chars().collect()).collect()
});

/// `closure(AM | PM)` accepts `v`.
fn suffix_ok(v: &[char]) -> bool {
    let mut reach = vec![false; v.len() + 1];
    reach[0] = true;
    for i in 0..v.len() {
        if !reach[i] {
            continue;
        }
        for s in SUFFIXES.iter() {
            if v[i..].starts_with(s) {
                reach[i + s.len()] = true;
            }
        }
    }
    reach[v.len()]
}

/// The field sequence `kinds` (a trailing suffix already checked) is `graph_regular | graph_back_count`; `is_cha`
/// tells, per field, whether its value is `差` (a `V` must be).
fn accepts(kinds: &[K], is_cha: impl Iterator<Item = bool>) -> bool {
    use K::*;
    let kinds = kinds.strip_suffix(&[Suffix]).unwrap_or(kinds);
    let regular = matches!(kinds, [H] | [M] | [S] | [H, M, S] | [H, M] | [H, S] | [M, S]);
    if regular {
        return true;
    }
    let back = {
        let k = kinds.strip_prefix(&[V]).unwrap_or(kinds);
        let k = k.strip_prefix(&[H]);
        k.is_some_and(|k| {
            let k = k.strip_prefix(&[V]).unwrap_or(k);
            matches!(k, [M] | [S] | [M, S])
        })
    };
    back && kinds.iter().zip(is_cha).all(|(&k, cha)| k != V || cha)
}

/// [`time`] on parsed fields whose values hold no `"`: `Some(accepted)`, or `None` for a key [`time`] does not read
/// or a suffix (checked by [`time`]).
pub fn direct(fields: &[(&str, &str)], out: &mut String) -> Option<bool> {
    let mut kinds = [K::H; 8];
    if fields.len() > kinds.len() {
        return None;
    }
    for (slot, (key, _)) in kinds.iter_mut().zip(fields) {
        *slot = match *key {
            "hours" => K::H,
            "minutes" => K::M,
            "seconds" => K::S,
            "morphosyntactic_features" => K::V,
            _ => return None,
        };
    }
    if !accepts(&kinds[..fields.len()], fields.iter().map(|(_, v)| *v == "差")) {
        return Some(false);
    }
    for (i, (&k, (_, v))) in kinds.iter().zip(fields).enumerate() {
        out.push_str(v);
        if k == K::V && i == 0 {
            out.push(' ');
        }
    }
    Some(true)
}

pub fn time(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    // every accepted field sequence has at most 8 fields, so a longer one is rejected as soon as it overflows
    let mut buf: [(K, &[char]); 8] = [(K::H, &[]); 8];
    let mut n = 0;
    loop {
        let before = *c;
        c.ws();
        let Some(&(open, k)) = KEYS.iter().find(|(open, _)| c.peek(open)) else {
            *c = before;
            break;
        };
        let Some(v) = c.field(open) else {
            *c = save;
            return false;
        };
        if n == buf.len() {
            *c = save;
            return false;
        }
        buf[n] = (k, v);
        n += 1;
    }
    let f = &buf[..n];
    let mut kinds = [K::H; 8];
    for (slot, x) in kinds.iter_mut().zip(f) {
        *slot = x.0;
    }
    let suffix = (kinds[..n].last() == Some(&K::Suffix)).then(|| f[n - 1].1);
    if suffix.is_some_and(|v| !suffix_ok(v)) || !accepts(&kinds[..n], f.iter().map(|x| x.1 == ['差'])) {
        *c = save;
        return false;
    }
    for (i, &(k, v)) in f.iter().enumerate() {
        out.extend(v);
        if k == K::V && i == 0 {
            out.push(' ');
        }
    }
    true
}
