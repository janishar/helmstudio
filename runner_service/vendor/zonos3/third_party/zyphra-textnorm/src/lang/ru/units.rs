// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Counted nouns (units, currencies, quantity words) and agreement.

use super::number::Gender;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Form {
    One,
    Few,
    Many,
    Gen,
}

#[derive(Clone, Debug)]
pub struct Noun {
    pub written: String,
    pub gender: Gender,
    pub one: String,
    pub few: String,
    pub many: String,
    pub genitive: String,
    pub acc: String,
}

impl Noun {
    pub fn form(&self, f: Form) -> &str {
        match f {
            Form::One => &self.one,
            Form::Few => &self.few,
            Form::Many => &self.many,
            Form::Gen => &self.genitive,
        }
    }
}

fn parse_gender(s: &str) -> Gender {
    match s {
        "fem" => Gender::Fem,
        "neut" => Gender::Neut,
        _ => Gender::Masc,
    }
}

crate::labels!(RAW_UNITS, "ru/measure/units.tsv");
crate::labels!(RAW_MAJOR, "ru/currency/major.tsv");
crate::labels!(RAW_MINOR, "ru/currency/minor.tsv");
crate::labels!(RAW_MINOR_WRITTEN, "ru/currency/minor_written.tsv");

pub static UNITS: LazyLock<Vec<Noun>> = LazyLock::new(|| {
    RAW_UNITS
        .iter()
        .filter(|r| r.len() >= 6)
        .map(|r| Noun {
            written: r[0].clone(),
            gender: parse_gender(&r[1]),
            one: r[2].clone(),
            few: r[3].clone(),
            many: r[4].clone(),
            genitive: r[5].clone(),
            acc: if r.len() >= 7 && !r[6].is_empty() { r[6].clone() } else { r[2].clone() },
        })
        .collect()
});

pub static MAJOR_CURRENCIES: LazyLock<Vec<Noun>> = LazyLock::new(|| {
    RAW_MAJOR
        .iter()
        .filter(|r| r.len() >= 6)
        .map(|r| Noun {
            written: r[0].clone(),
            gender: parse_gender(&r[1]),
            one: r[2].clone(),
            few: r[3].clone(),
            many: r[4].clone(),
            genitive: r[5].clone(),
            acc: r[2].clone(),
        })
        .collect()
});

pub static MINOR_CURRENCIES: LazyLock<Vec<Noun>> = LazyLock::new(|| {
    RAW_MINOR
        .iter()
        .filter(|r| r.len() >= 5)
        .map(|r| Noun {
            written: r[0].clone(),
            gender: parse_gender(&r[1]),
            one: r[2].clone(),
            few: r[3].clone(),
            many: r[4].clone(),
            genitive: r[3].clone(),
            acc: r[2].clone(),
        })
        .collect()
});

pub static MINOR_WRITTEN_CURRENCIES: LazyLock<Vec<Noun>> = LazyLock::new(|| {
    RAW_MINOR_WRITTEN
        .iter()
        .filter(|r| r.len() >= 6)
        .map(|r| Noun {
            written: r[0].clone(),
            gender: parse_gender(&r[1]),
            one: r[2].clone(),
            few: r[3].clone(),
            many: r[4].clone(),
            genitive: r[5].clone(),
            acc: r[2].clone(),
        })
        .collect()
});

pub static QUANTITY_NOUNS: LazyLock<Vec<Noun>> = LazyLock::new(|| {
    vec![
        Noun {
            written: "тыс".into(),
            gender: Gender::Fem,
            one: "тысяча".into(),
            few: "тысячи".into(),
            many: "тысяч".into(),
            genitive: "тысячи".into(),
            acc: "тысяча".into(),
        },
        Noun {
            written: "тыс.".into(),
            gender: Gender::Fem,
            one: "тысяча".into(),
            few: "тысячи".into(),
            many: "тысяч".into(),
            genitive: "тысячи".into(),
            acc: "тысяча".into(),
        },
        Noun {
            written: "тысяча".into(),
            gender: Gender::Fem,
            one: "тысяча".into(),
            few: "тысячи".into(),
            many: "тысяч".into(),
            genitive: "тысячи".into(),
            acc: "тысяча".into(),
        },
        Noun {
            written: "тысячи".into(),
            gender: Gender::Fem,
            one: "тысяча".into(),
            few: "тысячи".into(),
            many: "тысяч".into(),
            genitive: "тысячи".into(),
            acc: "тысяча".into(),
        },
        Noun {
            written: "тысяч".into(),
            gender: Gender::Fem,
            one: "тысяча".into(),
            few: "тысячи".into(),
            many: "тысяч".into(),
            genitive: "тысячи".into(),
            acc: "тысяча".into(),
        },
        Noun {
            written: "млн".into(),
            gender: Gender::Masc,
            one: "миллион".into(),
            few: "миллиона".into(),
            many: "миллионов".into(),
            genitive: "миллиона".into(),
            acc: "миллион".into(),
        },
        Noun {
            written: "млн.".into(),
            gender: Gender::Masc,
            one: "миллион".into(),
            few: "миллиона".into(),
            many: "миллионов".into(),
            genitive: "миллиона".into(),
            acc: "миллион".into(),
        },
        Noun {
            written: "миллион".into(),
            gender: Gender::Masc,
            one: "миллион".into(),
            few: "миллиона".into(),
            many: "миллионов".into(),
            genitive: "миллиона".into(),
            acc: "миллион".into(),
        },
        Noun {
            written: "миллиона".into(),
            gender: Gender::Masc,
            one: "миллион".into(),
            few: "миллиона".into(),
            many: "миллионов".into(),
            genitive: "миллиона".into(),
            acc: "миллион".into(),
        },
        Noun {
            written: "миллионов".into(),
            gender: Gender::Masc,
            one: "миллион".into(),
            few: "миллиона".into(),
            many: "миллионов".into(),
            genitive: "миллиона".into(),
            acc: "миллион".into(),
        },
        Noun {
            written: "млрд".into(),
            gender: Gender::Masc,
            one: "миллиард".into(),
            few: "миллиарда".into(),
            many: "миллиардов".into(),
            genitive: "миллиарда".into(),
            acc: "миллиард".into(),
        },
        Noun {
            written: "млрд.".into(),
            gender: Gender::Masc,
            one: "миллиард".into(),
            few: "миллиарда".into(),
            many: "миллиардов".into(),
            genitive: "миллиарда".into(),
            acc: "миллиард".into(),
        },
        Noun {
            written: "миллиард".into(),
            gender: Gender::Masc,
            one: "миллиард".into(),
            few: "миллиарда".into(),
            many: "миллиардов".into(),
            genitive: "миллиарда".into(),
            acc: "миллиард".into(),
        },
        Noun {
            written: "миллиарда".into(),
            gender: Gender::Masc,
            one: "миллиард".into(),
            few: "миллиарда".into(),
            many: "миллиардов".into(),
            genitive: "миллиарда".into(),
            acc: "миллиард".into(),
        },
        Noun {
            written: "миллиардов".into(),
            gender: Gender::Masc,
            one: "миллиард".into(),
            few: "миллиарда".into(),
            many: "миллиардов".into(),
            genitive: "миллиарда".into(),
            acc: "миллиард".into(),
        },
    ]
});

pub struct SuffixIndex(HashMap<char, Vec<&'static Noun>>);
impl SuffixIndex {
    fn new(nouns: &'static [Noun]) -> Self {
        let mut index: HashMap<char, Vec<&'static Noun>> = HashMap::new();
        for noun in nouns {
            if let Some(c) = noun.written.chars().last() {
                index.entry(c).or_default().push(noun);
            }
        }
        Self(index)
    }
    pub fn matching(&self, text: &str) -> &[&'static Noun] {
        text.chars().last().and_then(|c| self.0.get(&c)).map_or(&[], Vec::as_slice)
    }
}
pub static UNIT_SUFFIXES: LazyLock<SuffixIndex> = LazyLock::new(|| SuffixIndex::new(&UNITS));
pub static QUANTITY_SUFFIXES: LazyLock<SuffixIndex> = LazyLock::new(|| SuffixIndex::new(&QUANTITY_NOUNS));
pub static MAJOR_SUFFIXES: LazyLock<SuffixIndex> = LazyLock::new(|| SuffixIndex::new(&MAJOR_CURRENCIES));
pub static MINOR_WRITTEN_SUFFIXES: LazyLock<SuffixIndex> =
    LazyLock::new(|| SuffixIndex::new(&MINOR_WRITTEN_CURRENCIES));

pub struct EdgeIndex {
    first: HashMap<char, Vec<(usize, &'static Noun)>>,
    last: HashMap<char, Vec<(usize, &'static Noun)>>,
}
impl EdgeIndex {
    fn new(nouns: &'static [Noun]) -> Self {
        let mut first: HashMap<char, Vec<(usize, &'static Noun)>> = HashMap::new();
        let mut last: HashMap<char, Vec<(usize, &'static Noun)>> = HashMap::new();
        for (i, noun) in nouns.iter().enumerate() {
            if let Some(c) = noun.written.chars().next() {
                first.entry(c).or_default().push((i, noun));
            }
            if let Some(c) = noun.written.chars().last() {
                last.entry(c).or_default().push((i, noun));
            }
        }
        Self { first, last }
    }
    pub fn has_first(&self, c: char) -> bool {
        self.first.contains_key(&c)
    }
    pub fn starts_at(&self, text: &[char], start: usize) -> bool {
        text.get(start).and_then(|first| self.first.get(first)).is_some_and(|rows| {
            rows.iter().any(|(_, noun)| {
                let mut input = text[start..].iter().copied();
                noun.written.chars().all(|c| input.next() == Some(c))
            })
        })
    }
    pub fn matching(&self, text: &str) -> impl Iterator<Item = &'static Noun> + '_ {
        let a = text.chars().next().and_then(|c| self.first.get(&c)).map_or(&[][..], Vec::as_slice);
        let b = text.chars().last().and_then(|c| self.last.get(&c)).map_or(&[][..], Vec::as_slice);
        let (mut i, mut j) = (0, 0);
        std::iter::from_fn(move || match (a.get(i), b.get(j)) {
            (Some(&(x, noun)), Some(&(y, _))) if x == y => {
                i += 1;
                j += 1;
                Some(noun)
            }
            (Some(&(x, noun)), Some(&(y, _))) if x < y => {
                i += 1;
                Some(noun)
            }
            (Some(_), Some(&(_, noun))) => {
                j += 1;
                Some(noun)
            }
            (Some(&(_, noun)), None) => {
                i += 1;
                Some(noun)
            }
            (None, Some(&(_, noun))) => {
                j += 1;
                Some(noun)
            }
            (None, None) => None,
        })
    }
}
pub static MAJOR_EDGES: LazyLock<EdgeIndex> = LazyLock::new(|| EdgeIndex::new(&MAJOR_CURRENCIES));
pub static MINOR_EDGES: LazyLock<EdgeIndex> = LazyLock::new(|| EdgeIndex::new(&MINOR_CURRENCIES));

// A space after a completed lexical fragment can only lead onward through a
// quantity, a currency (including major/minor amounts), or a multiword unit
// or currency. Store source-derived suffixes, not input-dependent results.
static SPACE_PREDECESSORS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut words: HashMap<char, Vec<&'static str>> = HashMap::new();
    let mut add = |word: &'static str| {
        if let Some(last) = word.chars().last() {
            words.entry(last).or_default().push(word);
        }
    };
    for noun in UNITS
        .iter()
        .chain(MAJOR_CURRENCIES.iter())
        .chain(MINOR_CURRENCIES.iter())
        .chain(MINOR_WRITTEN_CURRENCIES.iter())
    {
        for (i, c) in noun.written.char_indices() {
            if c == ' ' && i > 0 {
                add(&noun.written[..i]);
            }
        }
    }
    for noun in MAJOR_CURRENCIES
        .iter()
        .chain(MINOR_CURRENCIES.iter())
        .chain(MINOR_WRITTEN_CURRENCIES.iter())
        .chain(QUANTITY_NOUNS.iter())
    {
        add(noun.written.as_str());
    }
    for word in ["k", "K", "к", "К", "M", "B", "bn"] {
        add(word);
    }
    words
});
pub fn lexical_space_continuation(prefix: &str) -> bool {
    prefix
        .chars()
        .last()
        .and_then(|last| SPACE_PREDECESSORS.get(&last))
        .is_some_and(|words| words.iter().any(|word| prefix.ends_with(word)))
}

static GRAMMAR_CHARS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    UNITS
        .iter()
        .chain(MAJOR_CURRENCIES.iter())
        .chain(MINOR_CURRENCIES.iter())
        .chain(MINOR_WRITTEN_CURRENCIES.iter())
        .chain(QUANTITY_NOUNS.iter())
        .flat_map(|noun| noun.written.chars())
        .collect()
});
pub fn compound_char(c: char) -> bool {
    c.is_ascii_alphanumeric()
        || ('а'..='я').contains(&c)
        || ('А'..='Я').contains(&c)
        || matches!(c, 'ё' | 'Ё' | ' ' | '\u{a0}' | '.' | ',' | '/' | '-' | '–' | '~' | '±')
        || GRAMMAR_CHARS.contains(&c)
}

pub fn agree(spoken_number: &str, form: Form, noun: &Noun) -> String {
    let replacement = match (noun.gender, form) {
        (Gender::Fem, Form::One) => Some(("один", "одна")),
        (Gender::Fem, Form::Few) => Some(("два", "две")),
        (Gender::Fem, Form::Gen) => Some(("одного", "одной")),
        (Gender::Neut, Form::One) => Some(("один", "одно")),
        _ => None,
    };
    let (prefix, last) = replacement
        .and_then(|(from, to)| {
            spoken_number
                .strip_suffix(from)
                .filter(|prefix| prefix.is_empty() || prefix.ends_with(' '))
                .map(|prefix| (prefix, to))
        })
        .unwrap_or((spoken_number, ""));
    let noun_form = noun.form(form);
    let mut out = String::with_capacity(prefix.len() + last.len() + 1 + noun_form.len());
    out.push_str(prefix);
    out.push_str(last);
    out.push(' ');
    out.push_str(noun_form);
    out
}

pub fn is_time_unit(written: &str) -> bool {
    matches!(written, "ч" | "h" | "мин" | "min" | "сек" | "сек." | "s" | "sec" | "мс" | "ms")
}

pub fn per_unit_phrase(noun: &Noun) -> String {
    if is_time_unit(&noun.written) { format!("в {}", noun.acc) } else { format!("на {}", noun.acc) }
}
