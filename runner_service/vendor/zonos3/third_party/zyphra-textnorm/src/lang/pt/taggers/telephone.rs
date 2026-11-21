//! `pt/taggers/telephone.py`: `[country_code] number_part [extension]`, digits spoken one by one.

use std::sync::LazyLock;

use super::cardinal::NUM;
use crate::lang::pt::graph_utils::{SpanTagger, words_while};
use crate::text::{Cost, cost, is_digit, is_nemo_space};

crate::tsv!(IP_PROMPT, "pt/telephone/ip_prompt.tsv");
crate::tsv!(TEL_PROMPT, "pt/telephone/telephone_prompt.tsv");

static WORDS: LazyLock<[String; 10]> = LazyLock::new(|| {
    let mut w: [String; 10] = Default::default();
    w[0] = NUM.zero.clone();
    for d in 1..10 {
        w[d] = NUM.digit[d].clone();
    }
    w
});

/// Parsing state: `out` is the field text being written.
struct P<'a> {
    s: &'a [char],
    i: usize,
}

impl P<'_> {
    fn digits(&mut self, n: usize, out: &mut String) -> bool {
        if self.i + n > self.s.len() || !self.s[self.i..self.i + n].iter().all(|c| is_digit(*c)) {
            return false;
        }
        for k in 0..n {
            if k > 0 {
                out.push(' ');
            }
            out.push_str(&WORDS[(self.s[self.i + k] as u8 - b'0') as usize]);
        }
        self.i += n;
        true
    }
    fn eat(&mut self, c: char) -> bool {
        if self.s.get(self.i) == Some(&c) {
            self.i += 1;
            true
        } else {
            false
        }
    }
    fn eat_str(&mut self, w: &str) -> bool {
        match crate::text::match_str(self.s, self.i, w) {
            Some(n) => {
                self.i += n;
                true
            }
            None => false,
        }
    }
    fn ws(&mut self) {
        while self.i < self.s.len() && is_nemo_space(self.s[self.i]) {
            self.i += 1;
        }
    }
    fn sep(&mut self) -> bool {
        self.eat('-') || self.eat('.')
    }
}

/// `number_part` alternatives from position `i`; each `(end, cost, text)`.
fn number_parts(s: &[char], i: usize) -> Vec<(usize, Cost, String)> {
    let mut res = Vec::new();
    // area_code + ws* + n(5|4) + sep + n(4)
    for paren in [true, false] {
        for mid in [5, 4] {
            let mut p = P { s, i };
            let mut o = String::new();
            if paren && !p.eat('(') {
                continue;
            }
            if !p.digits(2, &mut o) || (paren && !p.eat(')')) {
                continue;
            }
            p.ws();
            o.push(' ');
            if !p.digits(mid, &mut o) || !p.sep() {
                continue;
            }
            o.push(' ');
            if p.digits(4, &mut o) {
                res.push((p.i, 0, o));
            }
        }
    }
    for first in [5, 4, 3] {
        let mut p = P { s, i };
        let mut o = String::new();
        if p.digits(first, &mut o) && p.sep() {
            o.push(' ');
            if p.digits(4, &mut o) {
                res.push((p.i, 0, o));
            }
        }
    }
    // ip_graph: 1-3 digits, then three times "." 1-3 digits (+0.01)
    let mut p = P { s, i };
    let mut o = String::new();
    let mut ok = true;
    for g in 0..4 {
        if g > 0 {
            if !p.eat('.') {
                ok = false;
                break;
            }
            o.push_str(" ponto ");
        }
        let n = s[p.i..].iter().take(3).take_while(|c| is_digit(**c)).count();
        // digit_to_str_graph accepts 1-3 digits; a longer run cannot continue with "."
        if n == 0 || !p.digits(n, &mut o) {
            ok = false;
            break;
        }
    }
    if ok {
        res.push((p.i, cost(0.01), o));
    }
    res
}

/// `"+" country_digits` (1-3 digits, each length a separate path) at `i`: `(end, text)`.
fn plus_country(s: &[char], i: usize, prefix: &str, out: &mut Vec<(usize, String)>) {
    if s.get(i) != Some(&'+') {
        return;
    }
    for n in 1..=3 {
        let mut p = P { s, i: i + 1 };
        let mut o = format!("{prefix}mais ");
        if p.digits(n, &mut o) {
            out.push((p.i, o));
        }
    }
}

pub struct Telephone;

impl Telephone {
    /// `country_code_graph` alternatives (before `delete_optional_sep`), plus `(0, "")` for none.
    fn country_codes(s: &[char]) -> Vec<(usize, String)> {
        let mut raw = Vec::new();
        plus_country(s, 0, "", &mut raw);
        for (row, e) in IP_PROMPT.prefix_matches(s, 0) {
            raw.push((e, row.value.clone()));
        }
        for (row, e) in TEL_PROMPT.prefix_matches(s, 0) {
            if s.get(e) == Some(&' ') {
                let prefix = format!("{} ", row.value);
                raw.push((e + 1, prefix.clone()));
                plus_country(s, e + 1, &prefix, &mut raw);
            }
        }
        let mut res = vec![(0, String::new())];
        for (e, o) in raw {
            if matches!(s.get(e), Some('-' | '.')) {
                res.push((e + 1, o.clone()));
            }
            res.push((e, o));
        }
        res
    }
}

impl SpanTagger for Telephone {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) || matches!(c, '(' | '+' | 'i' | 'e' | 't' | 'c' | 'l' | 'm') { 12 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        let c = text[start];
        if self.max_words(c) == 0 {
            return 0;
        }
        let mut prompt_words = 0;
        if c.is_alphabetic() {
            let e = IP_PROMPT
                .prefix_matches(text, start)
                .chain(TEL_PROMPT.prefix_matches(text, start))
                .map(|(_, e)| e)
                .max();
            let Some(e) = e else { return 0 };
            prompt_words = text[start..e].iter().filter(|c| **c == ' ').count() + 1;
        }
        let mut i = start;
        for _ in 0..prompt_words {
            while i < text.len() && !is_nemo_space(text[i]) {
                i += 1;
            }
            while i < text.len() && is_nemo_space(text[i]) {
                i += 1;
            }
        }
        if i >= text.len() {
            return prompt_words;
        }
        let telish = |w: &[char]| {
            w.iter().any(|c| is_digit(*c) || matches!(c, '(' | ')' | '+'))
                || ["ramal", "extensão", "ext."].iter().any(|k| k.chars().eq(w.iter().copied()))
        };
        prompt_words + words_while(text, i, 10, telish)
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        // every number_part form holds a `-` or `.` separator
        // and none holds a comma
        if !span.iter().any(|&c| c == '-' || c == '.') || span.contains(&',') {
            return None;
        }
        let mut best: Option<(Cost, String)> = None;
        let mut ccs = Self::country_codes(span);
        ccs.dedup();
        for (ci, cc) in ccs {
            let mut i = ci;
            if !cc.is_empty() || ci > 0 {
                while i < span.len() && is_nemo_space(span[i]) {
                    i += 1;
                }
            }
            for (ne, nc, np) in number_parts(span, i) {
                let mut tail = String::new();
                if ne != span.len() {
                    // extension
                    let mut p = P { s: span, i: ne };
                    p.ws();
                    let intro = if p.eat_str("ramal") {
                        "ramal "
                    } else if p.eat_str("extensão") || p.eat_str("ext.") {
                        "extensão "
                    } else {
                        continue;
                    };
                    p.ws();
                    let mut o = String::from(intro);
                    let mut groups = 0;
                    loop {
                        if groups > 0 {
                            o.push(' ');
                        }
                        if !p.digits(1, &mut o) {
                            break;
                        }
                        groups += 1;
                        if groups == 4 {
                            break;
                        }
                    }
                    if groups == 0 || p.i != span.len() {
                        continue;
                    }
                    tail = format!(" extension: \"{}\"", o.trim_end());
                }
                let mut tag = String::from("telephone { ");
                if ci > 0 {
                    tag.push_str(&format!("country_code: \"{cc}\" "));
                }
                tag.push_str(&format!("number_part: \"{np}\"{tail} }}"));
                if best.as_ref().is_none_or(|(bc, _)| nc < *bc) {
                    best = Some((nc, tag));
                }
            }
        }
        best
    }
}
