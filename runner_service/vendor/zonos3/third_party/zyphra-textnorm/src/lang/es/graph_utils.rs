//! `es/graph_utils.py`: the gender/apocope rewrites (on verbalized strings, `[EOS]` = end of the string) and
//! `roman_to_int`. Also used by the `it` port.

use crate::text::{Cost, cost};

crate::tsv!(NUM_DIGIT, "es/numbers/digit.tsv");
crate::tsv!(NUM_TEEN, "es/numbers/teen.tsv");
crate::tsv!(NUM_TIES, "es/numbers/ties.tsv");
crate::tsv!(NUM_TWENTIES, "es/numbers/twenties.tsv");
crate::tsv!(NUM_HUNDREDS, "es/numbers/hundreds.tsv");
crate::tsv!(NUM_ZERO, "es/numbers/zero.tsv");
crate::tsv!(ROMAN_DIGIT, "es/roman/digit.tsv");
crate::tsv!(ROMAN_TIES, "es/roman/ties.tsv");
crate::tsv!(ROMAN_HUNDREDS, "es/roman/hundreds.tsv");
crate::tsv!(ROMAN_THOUSANDS, "es/roman/thousands.tsv");

/// Every way `alts` (literal strings) matches at the start of `s`, as the remainders.
fn lits<'a, 'b>(s: &'a str, alts: impl IntoIterator<Item = &'b str>) -> Vec<&'a str> {
    alts.into_iter().filter_map(|a| s.strip_prefix(a)).collect()
}

fn hundreds_words() -> impl Iterator<Item = &'static str> {
    NUM_HUNDREDS.rows().iter().map(|r| r.key.as_str())
}

/// `one_to_one_hundred` (graph_utils.py): remainders after one match at the start of `s`.
fn one_to_one_hundred(s: &str) -> Vec<&str> {
    let mut out = Vec::new();
    for t in [&*NUM_DIGIT, &*NUM_TIES, &*NUM_TEEN, &*NUM_TWENTIES] {
        out.extend(lits(s, t.rows().iter().map(|r| r.key.as_str())));
    }
    out.extend(lits(s, ["uno"]));
    for r in lits(s, NUM_TIES.rows().iter().map(|r| r.key.as_str())) {
        if let Some(r) = r.strip_prefix(" y ") {
            out.extend(lits(r, NUM_DIGIT.rows().iter().map(|r| r.key.as_str())));
        }
    }
    out
}

fn opt_space_then<'a>(rems: Vec<&'a str>, f: impl Fn(&'a str) -> Vec<&'a str>) -> Vec<&'a str> {
    let mut out = rems.clone();
    for r in rems {
        if let Some(r) = r.strip_prefix(' ') {
            out.extend(f(r));
        }
    }
    out
}

fn is_end(r: &str, comma: bool) -> bool {
    r.is_empty() || r.starts_with('"') || (comma && r.starts_with(','))
}

/// Apply an obligatory left-to-right rewrite: at each byte position `rule(prefix_done, rest)` may return
/// `(consumed_bytes, replacement)`; contexts read the input (`rest`).
fn rewrite(s: &str, rule: impl Fn(&str) -> Option<(usize, &'static str)>) -> String {
    let mut out = String::with_capacity(s.len() + 4);
    let mut i = 0;
    while i < s.len() {
        if let Some((n, rep)) = rule(&s[i..]) {
            out.push_str(rep);
            i += n;
            continue;
        }
        let c = s[i..].chars().next().unwrap();
        out.push(c);
        i += c.len_utf8();
    }
    out
}

fn fem_hundred(w: &str) -> &'static str {
    match w {
        "doscientos" => "doscientas",
        "trescientos" => "trescientas",
        "cuatrocientos" => "cuatrocientas",
        "quinientos" => "quinientas",
        "seiscientos" => "seiscientas",
        "setecientos" => "setecientas",
        "ochocientos" => "ochocientas",
        _ => "novecientas",
    }
}

fn hundreds_rule(rest: &str, ctx: &dyn Fn(&str) -> bool) -> Option<(usize, &'static str)> {
    for w in hundreds_words() {
        if let Some(r) = rest.strip_prefix(w) {
            if ctx(r) {
                return Some((w.len(), fem_hundred(w)));
            }
        }
    }
    None
}

/// `fem_ones` (un|ún|uno → una) with right context `ctx`; longest τ first is irrelevant (contexts decide).
fn ones_rule(rest: &str, ctx: &dyn Fn(&str) -> bool) -> Option<(usize, &'static str)> {
    for w in ["uno", "un", "ún"] {
        if let Some(r) = rest.strip_prefix(w) {
            if ctx(r) {
                return Some((w.len(), "una"));
            }
        }
    }
    None
}

fn before_mil(r: &str) -> bool {
    let rems = lits(r, [" mil", " milésimo"]);
    let rems = opt_space_then(rems, |x| lits(x, hundreds_words()));
    let rems = opt_space_then(rems, one_to_one_hundred);
    rems.iter().any(|x| is_end(x, true))
}

fn before_double(r: &str) -> bool {
    let rems = opt_space_then(vec![r], one_to_one_hundred);
    rems.iter().any(|x| is_end(x, false))
}

/// `shift_cardinal_gender`.
pub fn shift_cardinal_gender(s: &str) -> String {
    // every rule rewrites a hundreds word ("…ientos"/"quinientos") or a form of "un"; without one it is the identity
    if !s.contains("ientos") && !s.contains("un") && !s.contains("ún") {
        return s.to_string();
    }
    let s = rewrite(s, |rest| hundreds_rule(rest, &before_mil));
    let s = rewrite(&s, |rest| hundreds_rule(rest, &before_double));
    rewrite(&s, |rest| ones_rule(rest, &|r| is_end(r, true)))
}

/// `shift_number_gender`.
pub fn shift_number_gender(s: &str) -> String {
    if !s.contains("ientos") && !s.contains("un") && !s.contains("ún") {
        return s.to_string();
    }
    let s = rewrite(s, |rest| hundreds_rule(rest, &|_| true));
    rewrite(&s, |rest| ones_rule(rest, &|r| r.is_empty() || r.starts_with(' ') || r.starts_with('"')))
}

/// `strip_cardinal_apocope`: un|ún → uno before `[EOS]` or `"`.
pub fn strip_cardinal_apocope(s: &str) -> String {
    if !s.contains("un") && !s.contains("ún") {
        return s.to_string();
    }
    rewrite(s, |rest| {
        for w in ["un", "ún"] {
            if let Some(r) = rest.strip_prefix(w) {
                if is_end(r, false) {
                    return Some((w.len(), "uno"));
                }
            }
        }
        None
    })
}

/// `add_cardinal_apocope_fem`: una → un, veintiuna → veintiún before `[EOS]` or `"`.
pub fn add_cardinal_apocope_fem(s: &str) -> String {
    rewrite(s, |rest| {
        if let Some(r) = rest.strip_prefix("veintiuna") {
            if is_end(r, false) {
                return Some((9, "veintiún"));
            }
        }
        if let Some(r) = rest.strip_prefix("una") {
            if is_end(r, false) {
                return Some((3, "un"));
            }
        }
        None
    })
}

/// `strip_accent`.
pub fn strip_accent(s: &str) -> String {
    s.chars()
        .map(|c| match c {
            'á' => 'a',
            'é' => 'e',
            'í' => 'i',
            'ó' => 'o',
            'ú' => 'u',
            c => c,
        })
        .collect()
}

/// `roman_to_int` input side: every reading of `s` (all upper or all lower case) as `(arabic digits, cost)`.
pub fn roman_to_int(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    for upper in [true, false] {
        let ok = s.chars().all(|c| if upper { c.is_ascii_uppercase() } else { c.is_ascii_lowercase() });
        if !ok || s.is_empty() {
            continue;
        }
        let low = s.to_ascii_lowercase();
        // places: thousands, hundreds, ties, digit; a leading group starts at its place, later ones optional (0.01)
        let tables = [&*ROMAN_THOUSANDS, &*ROMAN_HUNDREDS, &*ROMAN_TIES, &*ROMAN_DIGIT];
        for lead in 0..4 {
            go(&low, &tables, lead, true, String::new(), 0, &mut out);
        }
    }
    out
}

fn go(
    s: &str,
    tables: &[&crate::text::Table; 4],
    place: usize,
    first: bool,
    acc: String,
    c: Cost,
    out: &mut Vec<(String, Cost)>,
) {
    if place == 4 {
        if s.is_empty() {
            out.push((acc, c));
        }
        return;
    }
    for r in tables[place].rows() {
        if let Some(rest) = s.strip_prefix(r.key.as_str()) {
            go(rest, tables, place + 1, false, format!("{acc}{}", r.value), c + r.cost, out);
        }
    }
    if !first {
        go(s, tables, place + 1, false, format!("{acc}0"), c + cost(0.01), out);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn gender() {
        assert_eq!(shift_cardinal_gender("doscientos mil doscientos un"), "doscientas mil doscientas una");
        assert_eq!(shift_cardinal_gender("doscientos millones"), "doscientos millones");
        assert_eq!(shift_cardinal_gender("veintiún"), "veintiuna");
        assert_eq!(strip_cardinal_apocope("veintiún"), "veintiuno");
        assert_eq!(roman_to_int("XX"), vec![("20".to_string(), 10_000)]);
        assert_eq!(roman_to_int("xiv"), vec![("14".to_string(), 0)]);
    }
}
