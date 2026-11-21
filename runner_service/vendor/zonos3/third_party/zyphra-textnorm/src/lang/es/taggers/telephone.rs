//! `es/taggers/telephone.py` (deterministic).

use super::cardinal;
use crate::lang::es::{Lazy, Sink};
use crate::text::{Cost, cost, is_alpha, is_digit};

crate::tsv!(IP_PROMPT, "es/telephone/ip_prompt.tsv");
crate::tsv!(TEL_PROMPT, "es/telephone/telephone_prompt.tsv");

/// `single_digits` (un → uno, 0 → cero).
fn sd(c: char) -> Option<&'static str> {
    match c {
        '0' => Some("cero"),
        '1' => Some("uno"),
        _ if is_digit(c) => cardinal::digit(c),
        _ => None,
    }
}

fn is_sep(c: Option<&char>) -> bool {
    matches!(c, Some('-') | Some(' ') | Some('.'))
}

/// `delete_separator`: positions after an optional separator.
fn dsep(text: &[char], p: usize) -> Vec<usize> {
    if is_sep(text.get(p)) { vec![p, p + 1] } else { vec![p] }
}

/// `n` single digits, each followed by an inserted space (`closure(sd + " ", n, n)`), appended to `acc`.
fn digits(text: &[char], p: usize, n: usize, acc: &str) -> Option<(usize, String)> {
    let mut s = acc.to_string();
    for k in 0..n {
        s.push_str(sd(*text.get(p + k)?)?);
        s.push(' ');
    }
    Some((p + n, s))
}

fn area_code(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    for s in if text.get(p) == Some(&'(') { vec![p, p + 1] } else { vec![p] } {
        if let Some((e, d)) = digits(text, s, 3, "") {
            out.push((e, d.clone()));
            if text.get(e) == Some(&')') {
                out.push((e + 1, d));
            }
        }
    }
    out
}

/// Groups after the area code: sizes like [3, 3, 4] for 10 digits: last group ends without trailing space.
fn groups(text: &[char], starts: Vec<(usize, String)>, sizes: &[usize]) -> Vec<(usize, String)> {
    let mut cur = starts;
    for (i, &n) in sizes.iter().enumerate() {
        let last = i + 1 == sizes.len();
        let mut next = Vec::new();
        for (e, s) in cur {
            let ps = dsep(text, e);
            for p in ps {
                if let Some((e2, mut s2)) = digits(text, p, n, &s) {
                    if last {
                        s2.pop();
                    }
                    next.push((e2, s2));
                }
            }
        }
        cur = next;
    }
    cur
}

fn number_part(text: &[char], p: usize) -> Vec<(usize, String, Cost, i32)> {
    let mut out = Vec::new();
    let ac = area_code(text, p);
    // Ranks reproduce the oracle's equal-cost choices (es golden telephone rows; pinned in `tests` below): ip 0,
    // ten 10 (26 when it starts with "cero"), eight 20, nine 30, letters 40, +5 with an extension.
    for (e, s) in groups(text, ac.clone(), &[3, 4]) {
        out.push((e, s, 0, 10));
    }
    for (e, s) in groups(text, ac.clone(), &[3, 3]) {
        out.push((e, s, 0, 30));
    }
    let mut eight_starts = ac.clone();
    eight_starts.push((p, String::new()));
    for (e, s) in groups(text, eight_starts, &[4, 4]) {
        out.push((e, s, 0, 20));
    }
    // nums_w_letters: area "800" → "ochocientos " + sep? + number_words
    for (e, s) in &ac {
        if s != "ocho cero cero " {
            continue;
        }
        for q in dsep(text, *e) {
            for (we, w) in number_words(text, q) {
                out.push((we, format!("ochocientos {w}"), cost(0.01), 40));
            }
        }
    }
    // ip
    for (e, s) in ip(text, p) {
        out.push((e, s, 0, 0));
    }
    out
}

fn digit_to_str(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    let mut s = String::new();
    for k in 0..3 {
        let Some(w) = text.get(p + k).and_then(|c| sd(*c)) else {
            break;
        };
        if k > 0 {
            s.push(' ');
        }
        s.push_str(w);
        out.push((p + k + 1, s.clone()));
    }
    out
}

fn ip(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut cur = digit_to_str(text, p);
    for _ in 0..3 {
        let mut next = Vec::new();
        for (e, s) in cur {
            if text.get(e) == Some(&'.') {
                for (e2, s2) in digit_to_str(text, e + 1) {
                    next.push((e2, format!("{s} punto {s2}")));
                }
            }
        }
        cur = next;
    }
    cur
}

fn number_word(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    let mut s = String::new();
    for k in 0..8 {
        let Some(&c) = text.get(p + k) else { break };
        if let Some(w) = sd(c) {
            s.push_str(w);
        } else if is_alpha(c) {
            s.push(c);
        } else {
            break;
        }
        out.push((p + k + 1, s.clone()));
    }
    out
}

fn number_words(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    let mut cur = vec![(p, String::new())];
    for round in 0..3 {
        let mut next = Vec::new();
        for (e, s) in &cur {
            for (e2, w) in number_word(text, *e) {
                let full = format!("{s}{w}");
                out.push((e2, full.clone()));
                if round < 2 && is_sep(text.get(e2)) {
                    next.push((e2 + 1, format!("{full} ")));
                }
            }
        }
        cur = next;
    }
    out
}

fn country_code(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    let (s, pre) = if text.get(p) == Some(&'+') { (p + 1, "más ") } else { (p, "") };
    let mut bodies = Vec::new();
    if let Some(w) = text.get(s).and_then(|c| sd(*c)) {
        bodies.push((s + 1, w.to_string()));
    }
    if s + 2 <= text.len() && text[s..s + 2].iter().all(|c| is_digit(*c)) && text[s] != '0' {
        let b = [text[s] as u8, text[s + 1] as u8];
        if let Some(t) = cardinal::tens(&b) {
            bodies.push((s + 2, if t.ends_with(" y un") { format!("{t}o") } else { t }));
        }
    }
    if let Some((e, mut d)) = digits(text, s, 3, "") {
        d.pop();
        bodies.push((e, d));
    }
    for (e, b) in bodies {
        out.push((e, format!("{pre}{b}")));
    }
    out
}

fn country_code_graph(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut bodies = country_code(text, p);
    for (r, e) in IP_PROMPT.prefix_matches(text, p) {
        bodies.push((e, r.value.clone()));
    }
    for (r, e) in TEL_PROMPT.prefix_matches(text, p) {
        if text.get(e) == Some(&' ') {
            bodies.push((e + 1, format!("{} ", r.value)));
            for (ce, c) in country_code(text, e + 1) {
                bodies.push((ce, format!("{} {c}", r.value)));
            }
        }
    }
    let mut out = Vec::new();
    for (e, b) in bodies {
        for q in dsep(text, e) {
            out.push((q, format!("country_code: \"{b}\"")));
        }
    }
    out
}

fn ext(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    let mut ps = Vec::new();
    for a in dsep(text, p) {
        let bs = if crate::text::starts_with(text, a, "ext.") { vec![a, a + 4] } else { vec![a] };
        for b in bs {
            ps.extend(dsep(text, b));
        }
    }
    ps.sort_unstable();
    ps.dedup();
    for q in ps {
        let mut s = String::from("extensión ");
        for k in 0..4 {
            let Some(w) = text.get(q + k).and_then(|c| sd(*c)) else {
                break;
            };
            if k > 0 {
                s.push(' ');
            }
            s.push_str(w);
            if k >= 1 {
                out.push((q + k + 1, format!("extension: \"{s}\"")));
            }
        }
    }
    out
}

pub struct Telephone;

impl Lazy for Telephone {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        // every branch starts with a digit, "(", "+" or a prompt (llam…/marque/mi/el/la)
        if !(is_digit(text[start]) || matches!(text[start], '(' | '+' | 'l' | 'm' | 'e')) {
            return;
        }
        // cheap necessary condition: ≥ 8 digits, or an IP (3 dots) or "800" in the digit run, or a prompt
        if crate::text::is_alpha(text[start]) {
            if IP_PROMPT.prefix_matches(text, start).next().is_none()
                && TEL_PROMPT.prefix_matches(text, start).next().is_none()
            {
                return;
            }
        } else {
            let (mut digits, mut dots, mut e) = (0, 0, start);
            while e < text.len() && (is_digit(text[e]) || matches!(text[e], '.' | '-' | ' ' | '(' | ')' | '+')) {
                digits += is_digit(text[e]) as usize;
                dots += (text[e] == '.') as usize;
                e += 1;
            }
            let has_800 = text[start..e].windows(3).any(|w| w == ['8', '0', '0']);
            if digits < 8 && dots < 3 && !has_800 {
                return;
            }
        }
        let mut heads: Vec<(usize, String)> = vec![(start, String::new())];
        for (e, cc) in country_code_graph(text, start) {
            for q in dsep(text, e) {
                heads.push((q, format!("{cc} ")));
            }
        }
        for (hp, h) in heads {
            for (e, np, c, mut kind) in number_part(text, hp) {
                if kind == 10 && np.starts_with("cero ") {
                    kind = 26;
                }
                let base = format!("{h}number_part: \"{np}\"");
                out.push(e, cost(1.11) + c, kind, || format!("telephone {{ {base} }}"));
                for q in dsep(text, e) {
                    for (xe, x) in ext(text, q) {
                        out.push(xe, cost(1.11) + c, kind + 5, || format!("telephone {{ {base} {x} }}"));
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::lang::es::test_util::tags;

    // Equal-cost telephone readings; the ranks above reproduce the oracle (es golden telephone rows).
    #[test]
    fn ten_digits_beat_eight_plus_extension() {
        // alternatives: number_part ten digits (rank 10) vs eight digits + "extensión cinco uno" (rank 25)
        assert_eq!(
            tags("llámame al 3125577651"),
            "tokens { telephone { country_code: \"llámame al \" number_part: \"tres uno dos cinco cinco siete siete seis cinco uno\" } }"
        );
    }

    #[test]
    fn leading_zero_ten_digits_lose_to_eight_plus_extension() {
        // alternatives: ten digits starting "cero" (rank 26) vs eight digits + "extensión cuatro tres" (rank 25)
        assert_eq!(
            tags("0603768043"),
            "tokens { telephone { number_part: \"cero seis cero tres siete seis ocho cero\" extension: \"extensión cuatro tres\" } }"
        );
    }

    #[test]
    fn ip_beats_digit_groups() {
        // alternatives: ip (rank 0) vs country code "uno" + ip remainder (rank 0 + later candidate order)
        assert_eq!(
            tags("0.272.844.670"),
            "tokens { telephone { number_part: \"cero punto dos siete dos punto ocho cuatro cuatro punto seis siete cero\" } }"
        );
    }

    #[test]
    fn eight_digits_with_country_code() {
        // alternatives: country code "siete" + eleven digits (rank 20) vs eight digits + extension (rank 25)
        assert_eq!(
            tags("~1559 7174 3239 5814 en"),
            "tokens { name: \"~1559\" } tokens { telephone { country_code: \"siete\" number_part: \"uno siete cuatro tres dos tres nueve cinco ocho uno cuatro\" } } tokens { name: \"en\" }"
        );
    }
}
