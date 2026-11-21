// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use crate::lang::hu::graph_utils::is_hu_alpha;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};

pub const WEIGHT: Cost = cost(1.10);

const DIGITS: [&str; 10] = ["nulla", "egy", "kettő", "három", "négy", "öt", "hat", "hét", "nyolc", "kilenc"];

const DIGITS_INLINE: [&str; 10] = ["nulla", "egy", "két", "három", "négy", "öt", "hat", "hét", "nyolc", "kilenc"];

const TENS_EXACT: [&str; 10] =
    ["", "tíz", "húsz", "harminc", "negyven", "ötven", "hatvan", "hetven", "nyolcvan", "kilencven"];

const TENS_INLINE: [&str; 10] =
    ["", "tizen", "huszon", "harminc", "negyven", "ötven", "hatvan", "hetven", "nyolcvan", "kilencven"];

pub fn spell_up_to_999(n: u64, out: &mut String) {
    let hundreds = n / 100;
    let rem = n % 100;

    if hundreds > 0 {
        if hundreds == 1 {
            out.push_str("száz");
        } else {
            out.push_str(DIGITS_INLINE[hundreds as usize]);
            out.push_str("száz");
        }
    }

    if rem > 0 {
        let tens = rem / 10;
        let units = rem % 10;
        if tens == 0 {
            out.push_str(DIGITS[units as usize]);
        } else if units == 0 {
            out.push_str(TENS_EXACT[tens as usize]);
        } else {
            out.push_str(TENS_INLINE[tens as usize]);
            out.push_str(DIGITS[units as usize]);
        }
    } else if hundreds == 0 {
        out.push_str("nulla");
    }
}

pub fn spell(n: u64, out: &mut String) {
    spell_number(n as u128, out);
}

pub fn spell_number(n: u128, out: &mut String) {
    spell_number_impl(n, out, false);
}

fn spell_number_impl(mut n: u128, out: &mut String, counted: bool) {
    if n == 0 {
        out.push_str("nulla");
        return;
    }
    let mut groups = [0u64; 8];
    for group in &mut groups {
        *group = (n % 1000) as u64;
        n /= 1000;
    }
    const MAGNITUDES: [&str; 8] = ["", "ezer", "millió", "milliárd", "billió", "billiárd", "trillió", "trilliárd"];
    let mut written = false;
    for i in (0..8).rev() {
        let value = groups[i];
        if value == 0 {
            continue;
        }
        if written && !(i == 0 && groups[1] == 1) {
            out.push('-');
        }
        if i == 0 {
            spell_up_to_999(value, out);
        } else if i == 1 {
            if value != 1 {
                spell_up_to_999(value, out);
            }
            out.push_str("ezer");
        } else {
            if value != 1 {
                spell_up_to_999(value, out);
            } else if counted {
                out.push_str("egy");
            }
            out.push_str(MAGNITUDES[i]);
        }
        written = true;
    }
}

pub fn number_cost(mut n: u128) -> Cost {
    if n < 1_000_000 {
        return 0;
    }
    n /= 1_000_000;
    let mut result = 0;
    while n != 0 {
        if n % 1000 == 1 {
            result -= cost(0.001);
        }
        n /= 1000;
    }
    result
}

pub fn spell_counted(n: u64, out: &mut String) {
    spell_counted_number(n as u128, out);
}

pub fn spell_counted_number(n: u128, out: &mut String) {
    spell_number_impl(n, out, true);
}

pub fn from_suffix(cardinal_word: &str) -> String {
    if cardinal_word.ends_with('a') {
        let mut s = cardinal_word[..cardinal_word.len() - 1].to_string();
        s.push_str("ától");
        return s;
    }
    // Find last harmonizing vowel skipping consonants, '-', 'i', 'í'
    let mut back = false;
    for c in cardinal_word.chars().rev() {
        if "aáoóuú".contains(c) {
            back = true;
            break;
        }
        if "eéöőüű".contains(c) {
            back = false;
            break;
        }
    }
    if back { format!("{cardinal_word}tól") } else { format!("{cardinal_word}től") }
}

pub fn to_suffix(cardinal_word: &str) -> String {
    if cardinal_word.ends_with('a') {
        let mut s = cardinal_word[..cardinal_word.len() - 1].to_string();
        s.push_str("áig");
        return s;
    }
    format!("{cardinal_word}ig")
}

pub const CASE_SUFFIXES: [&str; 72] = [
    "ban", "ben", "ba", "be", "ra", "re", "ról", "ről", "tól", "től", "ig", "kor", "nál", "nél", "hoz", "hez", "höz",
    "ból", "ből", "nak", "nek", "on", "en", "ön", "n", "t", "at", "et", "ot", "öt", "val", "vel", "mal", "mel", "tal",
    "tel", "nal", "nel", "sal", "sel", "szal", "szel", "zal", "zel", "ral", "rel", "lal", "lel", "kal", "kel", "gyal",
    "gyel", "dal", "del", "cal", "cel", "szor", "szer", "ször", "szoros", "szeres", "szörös", "as", "es", "os", "ös",
    "ast", "est", "ost", "öst", "ért", "szál",
];

const STEM_FIXES: [(&str, &str); 15] = [
    ("háromas", "hármas"),
    ("háromast", "hármast"),
    ("háromat", "hármat"),
    ("hároman", "hárman"),
    ("hétes", "hetes"),
    ("hétest", "hetest"),
    ("hétet", "hetet"),
    ("kettőes", "kettes"),
    ("kettőest", "kettest"),
    ("kettőszer", "kétszer"),
    ("kettőszeres", "kétszeres"),
    ("kettően", "ketten"),
    ("ezeres", "ezres"),
    ("ezerest", "ezrest"),
    ("ezeret", "ezret"),
];

pub fn apply_stem_fixes(word: &str) -> String {
    for &(pat, rep) in &STEM_FIXES {
        if word.ends_with(pat) {
            let prefix = &word[..word.len() - pat.len()];
            return format!("{prefix}{rep}");
        }
    }
    word.to_string()
}

/// Enumerate the filtered cardinal graph's numeric endpoints.
pub fn numbers(text: &[char], start: usize) -> impl Iterator<Item = (usize, u128)> + '_ {
    let mut end = start;
    let mut value = 0u128;
    let mut grouped = false;
    let mut digits = 0usize;
    std::iter::from_fn(move || {
        if start >= text.len() || !text[start].is_ascii_digit() {
            return None;
        }
        if !grouped {
            while end < text.len() && text[end].is_ascii_digit() && end - start < 24 {
                value = value * 10 + (text[end] as u8 - b'0') as u128;
                end += 1;
                if text[start] != '0' || end == start + 1 {
                    return Some((end, value));
                }
            }
            grouped = true;
            digits = end - start;
            if digits > 3 || text[start] == '0' {
                return None;
            }
        }
        if digits + 3 > 24
            || !matches!(text.get(end), Some('.' | ' '))
            || end + 3 >= text.len()
            || !text[end + 1..end + 4].iter().all(|c| c.is_ascii_digit())
        {
            return None;
        }
        for &c in &text[end + 1..end + 4] {
            value = value * 10 + (c as u8 - b'0') as u128;
        }
        end += 4;
        digits += 3;
        Some((end, value))
    })
}

fn counted_number(n: u128) -> String {
    let mut word = String::new();
    spell_counted_number(n, &mut word);
    word
}

pub struct Cardinal;

impl Lazy for Cardinal {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let negative = matches!(text.get(start), Some('-' | '−'));
        let p = start + usize::from(negative);
        if !negative && text.get(p) == Some(&'0') {
            let mut end = p + 1;
            while end < text.len() && text[end].is_ascii_digit() {
                end += 1;
                out.push(end, WEIGHT, 0, || {
                    let words: Vec<&str> = text[p..end].iter().map(|c| DIGITS[(*c as u8 - b'0') as usize]).collect();
                    format!("cardinal {{ integer: \"{}\" }}", words.join(" "))
                });
            }
        }
        for (end, n) in numbers(text, p) {
            let weight = WEIGHT + number_cost(n);
            out.push(end, weight, 0, || {
                let mut word = String::new();
                spell_number(n, &mut word);
                if negative {
                    format!("cardinal {{ negative: \"true\" integer: \"{word}\" }}")
                } else {
                    format!("cardinal {{ integer: \"{word}\" }}")
                }
            });
            if text.get(end) == Some(&'-') {
                let begin = end + 1;
                let mut suffix_end = begin;
                while suffix_end < text.len() && is_hu_alpha(text[suffix_end]) {
                    suffix_end += 1;
                    let suffix: String = text[begin..suffix_end].iter().collect();
                    if CASE_SUFFIXES.contains(&suffix.as_str()) || matches!(suffix.as_str(), "féle" | "fajta" | "an") {
                        out.push(suffix_end, weight, 1, || {
                            let mut word = String::new();
                            spell_number(n, &mut word);
                            word.push_str(&suffix);
                            let word = apply_stem_fixes(&word);
                            if negative {
                                format!("cardinal {{ negative: \"true\" integer: \"{word}\" }}")
                            } else {
                                format!("cardinal {{ integer: \"{word}\" }}")
                            }
                        });
                    }
                }
            }
            if matches!(text.get(end), Some('-' | '–' | '—')) {
                let mut second = end + 1;
                let neg2 = matches!(text.get(second), Some('-' | '−'));
                second += usize::from(neg2);
                for (end2, n2) in numbers(text, second) {
                    out.push(end2, weight + number_cost(n2), 2, || {
                        let first = from_suffix(&counted_number(n));
                        let last = to_suffix(&counted_number(n2));
                        let sign1 = if negative { "mínusz " } else { "" };
                        let sign2 = if neg2 { "mínusz " } else { "" };
                        format!("cardinal {{ integer: \"{sign1}{first} {sign2}{last}\" }}")
                    });
                }
            }
        }
    }
}
