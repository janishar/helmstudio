//! Source whitelist and context-sensitive Roman readings.
use super::{cardinal, numeric::Reading, ordinal};
use std::collections::HashMap;
use std::sync::LazyLock;

const ABBR: &[(&str, &str)] = &[
    ("Dr.", "dokter"),
    ("dr.", "dokter"),
    ("Dr", "dokter"),
    ("dr", "dokter"),
    ("Dhr.", "de heer"),
    ("dhr.", "de heer"),
    ("Dhr", "de heer"),
    ("dhr", "de heer"),
    ("Mevr.", "mevrouw"),
    ("mevr.", "mevrouw"),
    ("Mevr", "mevrouw"),
    ("mevr", "mevrouw"),
    ("Prof.", "professor"),
    ("prof.", "professor"),
    ("Prof", "professor"),
    ("prof", "professor"),
    ("Ir.", "ingenieur"),
    ("ir.", "ingenieur"),
    ("Drs.", "doctorandus"),
    ("drs.", "doctorandus"),
    ("Nr.", "nummer"),
    ("nr.", "nummer"),
    ("No.", "nummer"),
    ("no.", "nummer"),
    ("blz.", "bladzijde"),
    ("p.", "pagina"),
    ("art.", "artikel"),
    ("par.", "paragraaf"),
    ("fig.", "figuur"),
    ("tab.", "tabel"),
    ("bijv.", "bijvoorbeeld"),
    ("bv.", "bijvoorbeeld"),
    ("d.w.z.", "dat wil zeggen"),
    ("o.a.", "onder andere"),
    ("m.b.t.", "met betrekking tot"),
    ("t.a.v.", "ter attentie van"),
    ("z.g.a.n.", "zo goed als nieuw"),
    ("z.s.m.", "zo snel mogelijk"),
    ("a.u.b.", "alstublieft"),
    ("etc.", "enzovoort"),
    ("enz.", "enzovoort"),
    ("i.v.m.", "in verband met"),
    ("t.o.v.", "ten opzichte van"),
    ("m.a.w.", "met andere woorden"),
    ("n.v.t.", "niet van toepassing"),
    ("ca.", "circa"),
    ("nl.", "namelijk"),
    ("resp.", "respectievelijk"),
    ("&", "en"),
    ("©", "copyright"),
    ("§", "paragraaf"),
    ("±", "plusminus"),
    ("~", "ongeveer"),
    ("CO2", "C O twee"),
    ("5G", "vijf g"),
    ("A4", "A vier"),
];

const fn max_abbr_bytes() -> usize {
    let mut i = 0;
    let mut max = 0;
    while i < ABBR.len() {
        let len = ABBR[i].0.len();
        if len > max {
            max = len;
        }
        i += 1;
    }
    max
}
static ABBR_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| ABBR.iter().copied().collect());
const ROMANS: [&str; 21] = [
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI",
];
const MONARCHS: &[&str] = &[
    "Willem",
    "Louis",
    "Lodewijk",
    "Filips",
    "Karel",
    "Hendrik",
    "Jan",
    "Albert",
    "Boudewijn",
    "Leopold",
    "Elizabeth",
    "Beatrix",
    "Wilhelmina",
    "Juliana",
    "Paus",
    "Benedictus",
    "Johannes",
    "Franciscus",
    "Alexander",
    "Peter",
];

pub(super) fn roman_prefix(s: &str) -> bool {
    ROMANS.iter().any(|r| r.starts_with(s))
}

pub(super) fn whitelist(s: &str) -> Option<&'static str> {
    ABBR_INDEX.get(s).copied()
}

pub(super) fn whitelist_chars(s: &[char]) -> Option<&'static str> {
    let mut bytes = [0; max_abbr_bytes()];
    let mut used = 0;
    for &c in s {
        let mut encoded = [0; 4];
        let piece = c.encode_utf8(&mut encoded).as_bytes();
        if used + piece.len() > bytes.len() {
            return None;
        }
        bytes[used..used + piece.len()].copy_from_slice(piece);
        used += piece.len();
    }
    whitelist(std::str::from_utf8(&bytes[..used]).ok()?)
}

pub(super) fn starts_spaced_chars(s: &[char]) -> bool {
    let matches = |word: &&str| word.chars().eq(s.iter().copied());
    MONARCHS.iter().any(matches)
        || ["Hoofdstuk", "hoofdstuk", "Deel", "deel", "Boek", "boek", "Klasse", "klasse", "jaren"].iter().any(matches)
        || ROMANS.iter().any(matches)
        || (s.last() == Some(&'e') && ROMANS.iter().any(|r| r.chars().eq(s[..s.len() - 1].iter().copied())))
}

pub(super) fn starts_spaced(s: &str) -> bool {
    MONARCHS.contains(&s)
        || matches!(s, "Hoofdstuk" | "hoofdstuk" | "Deel" | "deel" | "Boek" | "boek" | "Klasse" | "klasse" | "jaren")
        || ROMANS.contains(&s)
        || s.strip_suffix('e').is_some_and(|r| ROMANS.contains(&r))
}

/// RomanFst and OrdinalFst both accept contextual Roman forms, with different weights and casing.
pub(super) fn roman(s: &str, ordinal_branch: bool) -> Vec<Reading> {
    let mut out = Vec::new();
    if !ordinal_branch {
        for (k, v) in [("XL", "X L"), ("MIX", "M I X"), ("CD", "C D")] {
            if s == k {
                out.push((v.to_owned(), 0.9));
            }
        }
    }
    if let Some((ctx, r)) = s.split_once(' ') {
        if let Some(n) = ROMANS.iter().position(|x| *x == r).map(|i| (i + 1).to_string()) {
            if MONARCHS.contains(&ctx) && (!ordinal_branch || !matches!(ctx, "Alexander" | "Peter")) {
                if let Some(v) = ordinal::raw(&n) {
                    out.push((format!("{ctx} de {v}"), 0.95));
                }
            }
            let lower = ctx.to_ascii_lowercase();
            if matches!(ctx, "Hoofdstuk" | "hoofdstuk" | "Deel" | "deel" | "Boek" | "boek")
                || (!ordinal_branch && matches!(ctx, "Klasse" | "klasse"))
            {
                if let Some(v) = cardinal::raw(&n) {
                    out.push((format!("{} {v}", if ordinal_branch { ctx } else { &lower }), 0.96));
                }
            }
        }
    }
    if let Some(r) = s.strip_suffix(" eeuw") {
        let r = r.strip_suffix('e').unwrap_or(r);
        if let Some(n) = ROMANS.iter().position(|x| *x == r).map(|i| (i + 1).to_string()) {
            if let Some(v) = ordinal::raw(&n) {
                out.push((format!("{v} eeuw"), 0.97));
            }
        }
    }
    out
}

pub(super) fn ordinal(s: &str) -> Vec<Reading> {
    let mut out = roman(s, true);
    for (suffix, w) in [("ste", 1.0), ("de", 1.0), ("e", 1.0), (".", 1.05)] {
        if let Some(n) = s.strip_suffix(suffix) {
            for (v, c) in ordinal::paths(n) {
                out.push((v, w + c));
            }
        }
    }
    out
}
