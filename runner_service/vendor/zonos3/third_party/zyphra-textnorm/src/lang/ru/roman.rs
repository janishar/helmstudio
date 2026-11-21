// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Roman numeral classifier, ported from ru/taggers/roman.py.

use super::number::{OrdinalForm, ordinal};
use crate::lang::common::{possible_token_end, possible_token_start};
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

const ROMAN_VALUES: &[(usize, &str)] =
    &[(100, "C"), (90, "XC"), (50, "L"), (40, "XL"), (10, "X"), (9, "IX"), (5, "V"), (4, "IV"), (1, "I")];

pub fn int_to_roman(mut n: usize) -> String {
    let mut out = String::new();
    for &(val, sym) in ROMAN_VALUES {
        while n >= val {
            out.push_str(sym);
            n -= val;
        }
    }
    out
}

pub fn parse_roman(s: &str) -> Option<usize> {
    let mut node = 0;
    for c in s.chars() {
        node = *ROMANS.nodes[node].next.get(&c)?;
    }
    let value = ROMANS.nodes[node].value;
    (value != 0).then_some(value)
}

const STRUCTURAL_BEFORE: &[(&str, OrdinalForm)] = &[
    ("глава", OrdinalForm::FemNom),
    ("главы", OrdinalForm::FemGen),
    ("главе", OrdinalForm::FemGen),
    ("главу", OrdinalForm::FemAcc),
    ("часть", OrdinalForm::FemNom),
    ("части", OrdinalForm::FemGen),
    ("книга", OrdinalForm::FemNom),
    ("книги", OrdinalForm::FemGen),
    ("статья", OrdinalForm::FemNom),
    ("статьи", OrdinalForm::FemGen),
    ("сцена", OrdinalForm::FemNom),
    ("сцены", OrdinalForm::FemGen),
    ("ступени", OrdinalForm::FemGen),
    ("ступень", OrdinalForm::FemNom),
    ("том", OrdinalForm::MascNom),
    ("тома", OrdinalForm::MascGen),
    ("тому", OrdinalForm::MascDat),
    ("томе", OrdinalForm::MascPrep),
    ("раздел", OrdinalForm::MascNom),
    ("раздела", OrdinalForm::MascGen),
    ("разделе", OrdinalForm::MascPrep),
    ("параграф", OrdinalForm::MascNom),
    ("параграфа", OrdinalForm::MascGen),
    ("пункт", OrdinalForm::MascNom),
    ("пункта", OrdinalForm::MascGen),
    ("акт", OrdinalForm::MascNom),
    ("акта", OrdinalForm::MascGen),
    ("выпуск", OrdinalForm::MascNom),
    ("выпуска", OrdinalForm::MascGen),
    ("этап", OrdinalForm::MascNom),
    ("этапа", OrdinalForm::MascGen),
    ("уровень", OrdinalForm::MascNom),
    ("уровня", OrdinalForm::MascGen),
    ("тип", OrdinalForm::MascNom),
    ("типа", OrdinalForm::MascGen),
    ("класс", OrdinalForm::MascNom),
    ("класса", OrdinalForm::MascGen),
    ("действие", OrdinalForm::NeutNom),
    ("приложение", OrdinalForm::NeutNom),
];

const NOUNS_AFTER: &[(&str, OrdinalForm, &str)] = &[
    ("век", OrdinalForm::MascNom, "век"),
    ("века", OrdinalForm::MascGen, "века"),
    ("веку", OrdinalForm::MascDat, "веку"),
    ("веке", OrdinalForm::MascPrep, "веке"),
    ("веком", OrdinalForm::MascIns, "веком"),
    ("веков", OrdinalForm::PlGen, "веков"),
    ("векам", OrdinalForm::PlNom, "векам"),
    ("веках", OrdinalForm::PlGen, "веках"),
    ("в.", OrdinalForm::MascGen, "века"),
    ("вв.", OrdinalForm::PlGen, "веков"),
    ("столетие", OrdinalForm::NeutNom, "столетие"),
    ("столетия", OrdinalForm::MascGen, "столетия"),
    ("столетии", OrdinalForm::MascPrep, "столетии"),
    ("тысячелетие", OrdinalForm::NeutNom, "тысячелетие"),
    ("тысячелетия", OrdinalForm::MascGen, "тысячелетия"),
    ("тысячелетии", OrdinalForm::MascPrep, "тысячелетии"),
    ("съезд", OrdinalForm::MascNom, "съезд"),
    ("съезда", OrdinalForm::MascGen, "съезда"),
    ("созыв", OrdinalForm::MascNom, "созыв"),
    ("созыва", OrdinalForm::MascGen, "созыва"),
    ("квартал", OrdinalForm::MascNom, "квартал"),
    ("квартала", OrdinalForm::MascGen, "квартала"),
    ("квартале", OrdinalForm::MascPrep, "квартале"),
    ("степени", OrdinalForm::FemGen, "степени"),
    ("степень", OrdinalForm::FemNom, "степень"),
    ("мировая", OrdinalForm::FemNom, "мировая"),
    ("мировой", OrdinalForm::FemGen, "мировой"),
];

const MASC_MONARCHS: &[&str] = &[
    "Пётр",
    "Петр",
    "Александр",
    "Николай",
    "Павел",
    "Иван",
    "Иоанн",
    "Фёдор",
    "Федор",
    "Алексей",
    "Василий",
    "Дмитрий",
    "Лжедмитрий",
    "Людовик",
    "Карл",
    "Генрих",
    "Георг",
    "Эдуард",
    "Филипп",
    "Фридрих",
    "Вильгельм",
    "Франц",
    "Франциск",
    "Фердинанд",
    "Альфонсо",
    "Яков",
    "Ричард",
    "Наполеон",
    "Густав",
    "Харальд",
    "Олаф",
    "Кристиан",
    "Леопольд",
    "Альберт",
    "Константин",
    "Иоанн Павел",
    "Бенедикт",
    "Пий",
    "Лев",
    "Григорий",
    "Климент",
    "Иннокентий",
    "Урбан",
    "Сикст",
    "Бонифаций",
    "Рамсес",
    "Рамзес",
    "Птолемей",
    "Дарий",
    "Ксеркс",
    "Соломон",
    "Тигран",
    "Вахтанг",
    "Ираклий",
];

const FEM_MONARCHS: &[&str] =
    &["Екатерина", "Елизавета", "Анна", "Мария", "Маргарита", "Виктория", "Изабелла", "Кристина", "Жанна", "Софья"];

#[derive(Default)]
struct TrieNode {
    next: HashMap<char, usize>,
    value: usize,
}

struct Trie {
    nodes: Vec<TrieNode>,
}

impl Trie {
    fn new() -> Self {
        Self { nodes: vec![TrieNode::default()] }
    }

    fn insert(&mut self, key: &str, value: usize) {
        let mut node = 0;
        for c in key.chars() {
            node = if let Some(&next) = self.nodes[node].next.get(&c) {
                next
            } else {
                let next = self.nodes.len();
                self.nodes.push(TrieNode::default());
                self.nodes[node].next.insert(c, next);
                next
            };
        }
        self.nodes[node].value |= value;
    }
}

static ROMANS: LazyLock<Trie> = LazyLock::new(|| {
    let mut trie = Trie::new();
    for n in 1..=100 {
        trie.insert(&int_to_roman(n), n);
    }
    trie
});

// Terminal bits distinguish structural keys from monarch names and ordinary
// following nouns from the narrower century-range vocabulary.
static BEFORE: LazyLock<Trie> = LazyLock::new(|| {
    let mut trie = Trie::new();
    for &(key, _) in STRUCTURAL_BEFORE {
        for variant in [key.to_string(), capitalize(key), key.to_uppercase()] {
            trie.insert(&variant, 1);
        }
    }
    for &key in MASC_MONARCHS.iter().chain(FEM_MONARCHS) {
        trie.insert(key, 2);
        trie.insert(&key.to_uppercase(), 2);
    }
    trie
});

static AFTER: LazyLock<Trie> = LazyLock::new(|| {
    let mut trie = Trie::new();
    for &(key, _, _) in NOUNS_AFTER {
        for variant in [key.to_string(), capitalize(key), key.to_uppercase()] {
            trie.insert(&variant, 1);
        }
    }
    for key in ["вв.", "веков", "века", "веках"] {
        trie.insert(key, 2);
    }
    trie
});

fn emit_roman_ends(text: &[char], start: usize, out: &mut Vec<Candidate>, allowed: impl Fn(usize) -> bool) {
    let mut node = 0;
    for end in start..text.len() {
        let Some(&next) = ROMANS.nodes[node].next.get(&text[end]) else { break };
        node = next;
        let value = ROMANS.nodes[node].value;
        if value != 0 && allowed(value) && possible_token_end(text, end + 1) {
            out.push(Candidate { end: end + 1, cost: cost(1.005), rank: 0, tag: String::new() });
        }
    }
}

fn emit_after(text: &[char], start: usize, flag: usize, out: &mut Vec<Candidate>) {
    let mut node = 0;
    for end in start..text.len() {
        let Some(&next) = AFTER.nodes[node].next.get(&text[end]) else { break };
        node = next;
        if AFTER.nodes[node].value & flag != 0 && possible_token_end(text, end + 1) {
            out.push(Candidate { end: end + 1, cost: cost(1.005), rank: 0, tag: String::new() });
        }
    }
}

pub fn render_roman(s: &str) -> Option<String> {
    // 1. Keyed before
    for &(kw, form) in STRUCTURAL_BEFORE {
        for prefix in [kw, &capitalize(kw), &kw.to_uppercase()] {
            if let Some(rest) = s.strip_prefix(prefix) {
                if let Some(rom_str) = rest.strip_prefix(' ') {
                    if rom_str != "C" && rom_str != "D" && rom_str != "L" && rom_str != "M" {
                        if let Some(val) = parse_roman(rom_str) {
                            if (1..=100).contains(&val) {
                                let ord_sp = ordinal(val as u64, form);
                                return Some(format!("{prefix}\u{a0}{ord_sp}").replace(' ', "\u{a0}"));
                            }
                        }
                    }
                }
            }
        }
    }

    // 2. Section § IV
    if let Some(rest) = s.strip_prefix('§') {
        let rom_str = rest.strip_prefix(' ').unwrap_or(rest);
        if rom_str != "C" && rom_str != "D" && rom_str != "L" && rom_str != "M" {
            if let Some(val) = parse_roman(rom_str) {
                if (1..=100).contains(&val) {
                    let ord_sp = ordinal(val as u64, OrdinalForm::MascNom);
                    return Some(format!("параграф\u{a0}{ord_sp}").replace(' ', "\u{a0}"));
                }
            }
        }
    }

    // 3. Century range: XIX-XX вв., XX–XXI века
    if let Some((roms, noun_str)) = s.split_once(' ') {
        for sep in ['-', '–'] {
            if let Some((r1, r2)) = roms.split_once(sep) {
                if let (Some(v1), Some(v2)) = (parse_roman(r1), parse_roman(r2)) {
                    if (1..=30).contains(&v1) && (1..=30).contains(&v2) {
                        let noun_sp = match noun_str {
                            "вв." | "веков" => Some("веков"),
                            "века" => Some("века"),
                            "веках" => Some("веках"),
                            _ => None,
                        };
                        if let Some(spoken_noun) = noun_sp {
                            let o1 = ordinal(v1 as u64, OrdinalForm::MascGen);
                            let o2 = ordinal(v2 as u64, OrdinalForm::MascGen);
                            return Some(format!("{o1}\u{a0}{o2}\u{a0}{spoken_noun}").replace(' ', "\u{a0}"));
                        }
                    }
                }
            }
        }
    }

    // 4. Keyed after: XXI век, в.
    if let Some((rom_str, noun_str)) = s.split_once(' ') {
        if let Some(val) = parse_roman(rom_str) {
            if (1..=50).contains(&val) {
                for &(written, form, spoken_noun) in NOUNS_AFTER {
                    let variants = [
                        (written, spoken_noun),
                        (&capitalize(written), &capitalize(spoken_noun)),
                        (&written.to_uppercase(), &spoken_noun.to_uppercase()),
                    ];
                    for (w, sp) in variants {
                        if noun_str == w {
                            let ord_sp = ordinal(val as u64, form);
                            return Some(format!("{ord_sp}\u{a0}{sp}").replace(' ', "\u{a0}"));
                        }
                    }
                }
            }
        }
    }

    // 5. Monarchs & Popes
    for &m in MASC_MONARCHS {
        for prefix in [m, &m.to_uppercase()] {
            if let Some(rest) = s.strip_prefix(prefix) {
                if let Some(rom_str) = rest.strip_prefix(' ') {
                    if let Some(val) = parse_roman(rom_str) {
                        if (1..=39).contains(&val) {
                            let ord_sp = ordinal(val as u64, OrdinalForm::MascNom);
                            return Some(format!("{prefix}\u{a0}{ord_sp}").replace(' ', "\u{a0}"));
                        }
                    }
                }
            }
        }
    }
    for &m in FEM_MONARCHS {
        for prefix in [m, &m.to_uppercase()] {
            if let Some(rest) = s.strip_prefix(prefix) {
                if let Some(rom_str) = rest.strip_prefix(' ') {
                    if let Some(val) = parse_roman(rom_str) {
                        if (1..=39).contains(&val) {
                            let ord_sp = ordinal(val as u64, OrdinalForm::FemNom);
                            return Some(format!("{prefix}\u{a0}{ord_sp}").replace(' ', "\u{a0}"));
                        }
                    }
                }
            }
        }
    }

    None
}

fn capitalize(s: &str) -> String {
    let mut chars = s.chars();
    if let Some(first) = chars.next() { format!("{}{}", first.to_uppercase(), chars.as_str()) } else { String::new() }
}

pub struct RomanClassifier;

impl Classifier for RomanClassifier {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !possible_token_start(text, start) {
            return;
        }

        if text[start] == '§' {
            let mut pos = start + 1;
            if text.get(pos) == Some(&' ') {
                pos += 1;
            }
            emit_roman_ends(text, pos, out, |n| n != 50 && n != 100);
            return;
        }

        let mut node = 0;
        for end in start..text.len() {
            let Some(&next) = BEFORE.nodes[node].next.get(&text[end]) else { break };
            node = next;
            let flags = BEFORE.nodes[node].value;
            if flags != 0 && text.get(end + 1) == Some(&' ') {
                emit_roman_ends(text, end + 2, out, |n| {
                    (flags & 1 != 0 && n != 50 && n != 100) || (flags & 2 != 0 && n <= 39)
                });
            }
        }

        let mut first = 0;
        for end in start..text.len() {
            let Some(&next) = ROMANS.nodes[first].next.get(&text[end]) else { break };
            first = next;
            let value = ROMANS.nodes[first].value;
            if value == 0 {
                continue;
            }
            if value <= 50 && text.get(end + 1) == Some(&' ') {
                emit_after(text, end + 2, 1, out);
            }
            if value <= 30 && matches!(text.get(end + 1), Some('-' | '–')) {
                let mut second = 0;
                for last in end + 2..text.len() {
                    let Some(&next) = ROMANS.nodes[second].next.get(&text[last]) else { break };
                    second = next;
                    let value = ROMANS.nodes[second].value;
                    if value != 0 && value <= 30 && text.get(last + 1) == Some(&' ') {
                        emit_after(text, last + 2, 2, out);
                    }
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let s: String = text[start..candidate.end].iter().collect();
        if let Some(rendered) = render_roman(&s) {
            candidate.tag = format!("name: \"{rendered}\"");
        }
    }
}
