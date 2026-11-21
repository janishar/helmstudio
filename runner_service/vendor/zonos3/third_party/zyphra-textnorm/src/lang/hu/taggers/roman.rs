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

use crate::lang::hu::graph_utils::{hu_capitalize, is_hu_alpha, naive_inflector};
use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::taggers::ordinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.06);

pub fn int_to_roman(mut n: u32) -> String {
    const VALS: [(u32, &str); 13] = [
        (1000, "M"),
        (900, "CM"),
        (500, "D"),
        (400, "CD"),
        (100, "C"),
        (90, "XC"),
        (50, "L"),
        (40, "XL"),
        (10, "X"),
        (9, "IX"),
        (5, "V"),
        (4, "IV"),
        (1, "I"),
    ];
    let mut out = String::new();
    for &(val, sym) in &VALS {
        while n >= val {
            out.push_str(sym);
            n -= val;
        }
    }
    out
}

const KEY_WORDS: [&str; 23] = [
    "fejezet",
    "század",
    "rész",
    "kötet",
    "cikk",
    "szakasz",
    "paragrafus",
    "törvénycikk",
    "osztály",
    "évfolyam",
    "emelet",
    "kerület",
    "csoport",
    "fokozat",
    "világháború",
    "szimfónia",
    "felvonás",
    "jelenet",
    "tétel",
    "könyv",
    "korszak",
    "évszázad",
    "évezred",
];

const FIXED_KEY_WORDS: [&str; 15] = [
    "sz.",
    "§",
    "fej.",
    "ker.",
    "em.",
    "évf.",
    "bek.",
    "világháborúban",
    "világháborúig",
    "világháborútól",
    "században",
    "századi",
    "századtól",
    "századig",
    "fejezetben",
];

const ROYAL_NAMES: [&str; 37] = [
    "István",
    "Béla",
    "László",
    "Károly",
    "Lajos",
    "Mátyás",
    "András",
    "Endre",
    "Géza",
    "Imre",
    "Salamon",
    "Kálmán",
    "Zsigmond",
    "Ulászló",
    "Ferdinánd",
    "Lipót",
    "József",
    "Ferenc",
    "Miklós",
    "Sándor",
    "Pál",
    "János",
    "Péter",
    "György",
    "Henrik",
    "Eduárd",
    "Jakab",
    "Vilmos",
    "Frigyes",
    "Fülöp",
    "Alfonz",
    "Viktor",
    "Napóleon",
    "Erzsébet",
    "Mária",
    "Katalin",
    "Anna",
];

const FOREIGN_ROYALS: [&str; 11] =
    ["Louis", "Elizabeth", "Charles", "Henry", "George", "Edward", "Philip", "Wilhelm", "Karl", "Gustaf", "Juan"];

struct RomanTables {
    kw_map: HashMap<String, String>,
    royal_set: HashSet<String>,
    roman_to_ord_100: HashMap<String, String>,
    roman_to_card_100: HashMap<String, String>,
}

static TABLES: LazyLock<RomanTables> = LazyLock::new(|| {
    let mut kw_map = HashMap::new();
    kw_map.insert("sz.".to_string(), "szám".to_string());
    kw_map.insert("§".to_string(), "paragrafus".to_string());
    kw_map.insert("fej.".to_string(), "fejezet".to_string());
    kw_map.insert("ker.".to_string(), "kerület".to_string());
    kw_map.insert("em.".to_string(), "emelet".to_string());
    kw_map.insert("évf.".to_string(), "évfolyam".to_string());
    kw_map.insert("bek.".to_string(), "bekezdés".to_string());

    for &f in &FIXED_KEY_WORDS {
        kw_map.entry(f.to_string()).or_insert_with(|| f.to_string());
    }

    for &w in &KEY_WORDS {
        kw_map.entry(w.to_string()).or_insert_with(|| w.to_string());
        kw_map.entry(hu_capitalize(w)).or_insert_with(|| hu_capitalize(w));
        for (_, infl) in naive_inflector(".", w, false) {
            kw_map.entry(infl.clone()).or_insert_with(|| infl.clone());
            kw_map.entry(hu_capitalize(&infl)).or_insert_with(|| hu_capitalize(&infl));
        }
    }

    let mut royal_set = HashSet::new();
    for &r in &ROYAL_NAMES {
        royal_set.insert(r.trim().to_string());
    }

    let mut roman_to_ord_100 = HashMap::new();
    let mut roman_to_card_100 = HashMap::new();
    for n in 1..=100 {
        let r = int_to_roman(n);
        let mut card_sp = String::new();
        cardinal::spell(n as u64, &mut card_sp);
        let ord_sp = ordinal::cardinal_to_ordinal(&card_sp);
        roman_to_ord_100.insert(r.clone(), ord_sp);
        if r != "C" && r != "D" && r != "L" && r != "M" {
            roman_to_card_100.insert(r, card_sp);
        }
    }

    RomanTables { kw_map, royal_set, roman_to_ord_100, roman_to_card_100 }
});
static POSTPOSED_KEYWORDS: LazyLock<Vec<(String, String)>> =
    LazyLock::new(|| KEY_WORDS.iter().map(|&word| (word.to_owned(), hu_capitalize(word))).collect());
static POSTPOSED_INDEX: LazyLock<HashMap<char, Vec<(usize, bool)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(usize, bool)>> = HashMap::new();
    for (i, (lower, capital)) in POSTPOSED_KEYWORDS.iter().enumerate() {
        for (form, upper) in [(lower, false), (capital, true)] {
            if let Some(c) = form.chars().next() {
                index.entry(c).or_default().push((i, upper));
            }
        }
    }
    index
});

fn match_prefix(text: &[char], start: usize, key: &str) -> Option<usize> {
    let mut end = start;
    for c in key.chars() {
        if text.get(end) != Some(&c) {
            return None;
        }
        end += 1;
    }
    Some(end)
}

pub struct Roman;

impl Lazy for Roman {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }
        let first = text[start];
        if !is_hu_alpha(first) {
            return;
        }
        let roman_start = matches!(first, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M');
        let foreign_start = FOREIGN_ROYALS.iter().any(|word| word.starts_with(first));
        let keywords = POSTPOSED_INDEX.get(&first);
        if !roman_start && !foreign_start && keywords.is_none() {
            return;
        }

        // 1. Postposed: keyword / foreign royal + Roman numeral
        // Check foreign royal: "Louis XIV"
        for &fr in &FOREIGN_ROYALS {
            if let Some(mut p) = match_prefix(text, start, fr) {
                if p < text.len() && text[p] == ' ' {
                    p += 1;
                    let r_start = p;
                    while p < text.len() && matches!(text[p], 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M') {
                        p += 1;
                    }
                    if p > r_start {
                        let r_str: String = text[r_start..p].iter().collect();
                        if let Some(ord_str) = TABLES.roman_to_ord_100.get(&r_str) {
                            let mut end_pos = p;
                            if end_pos < text.len() && text[end_pos] == '.' {
                                end_pos += 1;
                            }
                            out.push(end_pos, WEIGHT, 0, || format!("name: \"{fr} {ord_str}\""));
                        }
                    }
                }
            }
        }

        // Check postposed keyword: "fejezet IV" -> "fejezet négy"
        for &(index, capital) in keywords.into_iter().flatten() {
            let (kw, kw_cap) = &POSTPOSED_KEYWORDS[index];
            let kw_form = if capital { kw_cap } else { kw };
            if let Some(mut p) = match_prefix(text, start, kw_form) {
                if p < text.len() && text[p] == ' ' {
                    p += 1;
                    let r_start = p;
                    while p < text.len() && matches!(text[p], 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M') {
                        p += 1;
                    }
                    if p > r_start {
                        let r_str: String = text[r_start..p].iter().collect();
                        if let Some(card_str) = TABLES.roman_to_card_100.get(&r_str) {
                            let mut end_pos = p;
                            if end_pos < text.len() && text[end_pos] == '.' {
                                end_pos += 1;
                            }
                            out.push(end_pos, WEIGHT, 0, || format!("name: \"{kw_form} {card_str}\""));
                        }
                    }
                }
            }
        }

        // 2. Preposed Roman Numeral: "IV. fejezet", "IV. Béla", "XXI század"
        let mut r_end = start;
        while r_end < text.len() && matches!(text[r_end], 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M') {
            r_end += 1;
        }

        if r_end > start {
            let r_str: String = text[start..r_end].iter().collect();
            if let Some(ord_str) = TABLES.roman_to_ord_100.get(&r_str) {
                // With dot: "IV. fejezet" or "IV. Béla"
                if r_end < text.len() && text[r_end] == '.' && r_end + 1 < text.len() && text[r_end + 1] == ' ' {
                    let after_space = r_end + 2;

                    // Match Royal Name
                    for royal in &TABLES.royal_set {
                        if let Some(end_pos) = match_prefix(text, after_space, royal) {
                            out.push(end_pos, WEIGHT, 0, || format!("name: \"{ord_str} {royal}\""));
                        }
                    }

                    // Match Key Word
                    for (kw_form, kw_expanded) in &TABLES.kw_map {
                        if let Some(end_pos) = match_prefix(text, after_space, kw_form) {
                            if kw_form.chars().last().is_some_and(is_hu_alpha)
                                && text.get(end_pos).copied().is_some_and(is_hu_alpha)
                            {
                                continue;
                            }
                            out.push(end_pos, WEIGHT, 0, || format!("name: \"{ord_str} {kw_expanded}\""));
                        }
                    }
                }

                // Without dot: "XXI század" (len >= 2 and != "XL")
                if r_str.len() >= 2 && r_str != "XL" && r_end < text.len() && text[r_end] == ' ' {
                    let after_space = r_end + 1;
                    for &kw in &KEY_WORDS {
                        if let Some(end_pos) = match_prefix(text, after_space, kw) {
                            out.push(end_pos, WEIGHT, 0, || format!("name: \"{ord_str} {kw}\""));
                        }
                    }
                }
            }
        }
    }
}
