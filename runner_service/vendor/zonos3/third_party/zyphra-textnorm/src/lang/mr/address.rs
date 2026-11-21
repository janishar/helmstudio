//! Keyword-gated address transducer from `measure.py`.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{cost, is_nemo_space};

const CONTEXT: &str = include_str!("../../../data/mr/address/context.tsv");
const LETTERS: &str = include_str!("../../../data/mr/address/letters.tsv");
const SYMBOLS: &str = include_str!("../../../data/mr/address/special_characters.tsv");
const CITIES: &str = include_str!("../../../data/mr/address/cities.tsv");
const STATES: &str = include_str!("../../../data/mr/address/states.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|s| s.trim_end_matches('\r').split_once('\t'))
}

pub(super) struct KeywordIndex {
    ascii: [Vec<&'static str>; 128],
    devanagari: [Vec<&'static str>; 128],
    other: std::collections::HashMap<char, Vec<&'static str>>,
}

impl KeywordIndex {
    pub(super) fn new() -> Self {
        Self {
            ascii: std::array::from_fn(|_| Vec::new()),
            devanagari: std::array::from_fn(|_| Vec::new()),
            other: Default::default(),
        }
    }

    pub(super) fn insert(&mut self, word: &'static str) {
        let Some(c) = word.chars().next() else { return };
        let n = c as usize;
        if n < 128 {
            self.ascii[n].push(word);
        } else if (0x0900..=0x097f).contains(&n) {
            self.devanagari[n - 0x0900].push(word);
        } else {
            self.other.entry(c).or_default().push(word);
        }
    }

    pub(super) fn get(&self, c: char) -> &[&'static str] {
        let n = c as usize;
        if n < 128 {
            &self.ascii[n]
        } else if (0x0900..=0x097f).contains(&n) {
            &self.devanagari[n - 0x0900]
        } else {
            self.other.get(&c).map(Vec::as_slice).unwrap_or(&[])
        }
    }
}

fn boundary(c: char) -> bool {
    is_nemo_space(c) || matches!(c, ',' | '.' | '।' | '\n' | '\r' | '\t')
}

pub(crate) fn keyword_bounds(s: &str) -> Option<(usize, usize)> {
    static KEYWORDS: std::sync::LazyLock<KeywordIndex> = std::sync::LazyLock::new(|| {
        let mut index = KeywordIndex::new();
        for word in CONTEXT.lines().map(|line| line.trim_end_matches('\r').split('\t').next().unwrap_or("")) {
            index.insert(word);
        }
        index
    });

    let mut words = 0;
    let mut in_word = false;
    let mut earliest: Option<usize> = None;
    let mut latest: Option<usize> = None;

    for (i, c) in s.char_indices() {
        if boundary(c) {
            in_word = false;
        } else if !in_word {
            if words > 5 {
                break;
            }
            for keyword in KEYWORDS.get(c) {
                if s[i..].starts_with(*keyword) {
                    let end = i + keyword.len();
                    earliest = Some(earliest.map_or(end, |old| old.min(end)));
                    let rest = &s[end..];
                    let mut last = end;
                    if rest.chars().next().is_some_and(boundary) {
                        let mut after_words = 0;
                        let mut after_word = false;
                        last = s.len();
                        for (j, ch) in rest.char_indices() {
                            if boundary(ch) {
                                after_word = false;
                            } else if !after_word {
                                after_words += 1;
                                if after_words > 5 {
                                    last = end + j;
                                    break;
                                }
                                after_word = true;
                            }
                        }
                    }
                    latest = Some(latest.map_or(last, |old| old.max(last)));
                }
            }
            words += 1;
            in_word = true;
        }
    }

    earliest.zip(latest)
}

static CITIES_MAP: std::sync::LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    std::sync::LazyLock::new(|| {
        let mut out = std::collections::HashMap::new();
        for line in CITIES.lines().chain(STATES.lines()) {
            let line = line.trim_end_matches('\r');
            let (k, v) = line.split_once('\t').unwrap_or((line, line));
            out.insert(k, v);
        }
        out
    });

struct CityIndex {
    cities: Vec<(&'static str, &'static str)>,
    ascii: [Vec<usize>; 128],
    devanagari: [Vec<usize>; 128],
    other: std::collections::HashMap<char, Vec<usize>>,
}

impl CityIndex {
    fn starts(&self, c: char) -> &[usize] {
        let n = c as usize;
        if n < 128 {
            &self.ascii[n]
        } else if (0x0900..=0x097f).contains(&n) {
            &self.devanagari[n - 0x0900]
        } else {
            self.other.get(&c).map(Vec::as_slice).unwrap_or(&[])
        }
    }
}

static CITY_INDEX: std::sync::LazyLock<CityIndex> = std::sync::LazyLock::new(|| {
    let cities: Vec<_> = CITIES_MAP.iter().map(|(&key, &value)| (key, value)).collect();
    let mut index = CityIndex {
        cities,
        ascii: std::array::from_fn(|_| Vec::new()),
        devanagari: std::array::from_fn(|_| Vec::new()),
        other: Default::default(),
    };
    for (i, &(key, _)) in index.cities.iter().enumerate() {
        if let Some(c) = key.chars().next() {
            let n = c as usize;
            if n < 128 {
                index.ascii[n].push(i);
            } else if (0x0900..=0x097f).contains(&n) {
                index.devanagari[n - 0x0900].push(i);
            } else {
                index.other.entry(c).or_default().push(i);
            }
        }
    }
    index
});

pub(crate) fn city_prefix_end(s: &str) -> Option<usize> {
    for i in std::iter::once(0).chain(s.match_indices(' ').map(|(i, _)| i + 1)) {
        let Some(c) = s[i..].chars().next() else { continue };
        {
            let cities = CITY_INDEX.starts(c);
            for &index in cities {
                let city = CITY_INDEX.cities[index].0;
                if s[i..].starts_with(city)
                    && s.match_indices(city).any(|(pos, _)| {
                        (pos == 0 || s[..pos].ends_with(' '))
                            && s[pos + city.len()..].chars().next().is_none_or(|ch| ch == ' ' || ch == ',')
                    })
                {
                    return Some(s.len());
                }
            }
        }
    }
    None
}

fn letter_word(c: char) -> Option<&'static str> {
    static LETTERS_MAP: std::sync::LazyLock<std::collections::HashMap<char, &'static str>> =
        std::sync::LazyLock::new(|| {
            let mut out = std::collections::HashMap::new();
            for (k, v) in rows(LETTERS) {
                if let Some(ch) = k.chars().next() {
                    out.insert(ch, v);
                    out.insert(ch.to_ascii_lowercase(), v);
                }
            }
            out
        });
    LETTERS_MAP.get(&c).copied()
}

fn symbol_word(c: char) -> Option<&'static str> {
    static SYMBOLS_MAP: std::sync::LazyLock<std::collections::HashMap<char, &'static str>> =
        std::sync::LazyLock::new(|| {
            let mut out = std::collections::HashMap::new();
            for (k, v) in rows(SYMBOLS) {
                if let Some(ch) = k.chars().next() {
                    out.insert(ch, v);
                }
            }
            out
        });
    SYMBOLS_MAP.get(&c).copied()
}

fn code_num(s: &str) -> Option<String> {
    let count = s.chars().count();
    if count >= 4 {
        numbers::digitwise(s)
    } else {
        numbers::graph(s).into_iter().min_by_key(|(_, c)| *c).map(|(v, _)| v)
    }
}

pub fn classify_structured(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let mut matching_cities = Vec::new();
    for i in std::iter::once(0).chain(s.match_indices(' ').map(|(i, _)| i + 1)) {
        let Some(c) = s[i..].chars().next() else { continue };
        {
            let cities = CITY_INDEX.starts(c);
            for &index in cities {
                if s[i..].starts_with(CITY_INDEX.cities[index].0) {
                    matching_cities.push(index);
                }
            }
        }
    }
    matching_cities.sort_unstable();
    matching_cities.dedup();
    for index in matching_cities {
        let (city, city_output) = CITY_INDEX.cities[index];
        for (pos, _) in s.match_indices(city) {
            let prefix = &s[..pos];
            if !prefix.is_empty() && !prefix.ends_with(' ') {
                continue;
            }
            let tail = &s[pos + city.len()..];
            let pin = if tail.is_empty() {
                None
            } else if let Some(pin) = tail.strip_prefix(' ') {
                if pin.chars().count() != 6 || !pin.chars().all(|c| numbers::digit(c).is_some()) {
                    continue;
                }
                Some(pin)
            } else {
                continue;
            };
            let mut count = 0;
            let mut first_numeric = false;
            let mut weight = 0;
            let mut valid = true;
            for part in prefix.split_terminator(' ') {
                let word = part.strip_suffix(',').unwrap_or(part);
                count += 1;
                if word.is_empty() {
                    valid = false;
                    break;
                }
                if word.chars().all(|c| numbers::digit(c).is_some()) {
                    if count == 1 {
                        first_numeric = true;
                    }
                    if word.chars().count() < 4 {
                        if word.chars().count() > 1 && word.chars().next().is_some_and(numbers::is_zero) {
                            valid = false;
                            break;
                        }
                        if let Some((_, c)) = numbers::graph(word).into_iter().min_by_key(|(_, c)| *c) {
                            weight += c;
                        } else {
                            valid = false;
                            break;
                        }
                    }
                } else if word
                    .chars()
                    .any(|c| numbers::digit(c).is_some() || is_nemo_space(c) || matches!(c, ',' | '.' | '।'))
                {
                    valid = false;
                    break;
                }
            }
            if !valid || count > if first_numeric { 6 } else { 5 } {
                continue;
            }
            out.push(Candidate {
                end,
                cost: cost(2.1) + weight,
                rank: 6,
                tag: super::lazy::tag(|| {
                    let mut text = String::new();
                    for part in prefix.split_terminator(' ') {
                        let (word, comma) = part.strip_suffix(',').map_or((part, ""), |w| (w, ","));
                        if word.chars().all(|c| numbers::digit(c).is_some()) {
                            text.push_str(&code_num(word).unwrap_or_default());
                        } else {
                            text.push_str(word);
                        }
                        text.push_str(comma);
                        text.push(' ');
                    }
                    text.push_str(city_output);
                    if let Some(pin) = pin {
                        text.push(' ');
                        text.push_str(&numbers::digitwise(pin).unwrap_or_default());
                    }
                    format!("measure {{ units: \"address\" cardinal {{ integer: \"{text}\" }} preserve_order: true }}")
                }),
            });
        }
    }
}

#[derive(Clone, Copy)]
enum AddressPart {
    Space,
    Comma,
    Other,
    Latin,
    Letter,
    Symbol,
    Number,
    Ordinal,
    Serial,
}

fn keyword_pattern(s: &str) -> bool {
    fn window(s: &str) -> bool {
        s.split(boundary).filter(|word| !word.is_empty()).count() <= 5
    }
    CONTEXT.lines().any(|line| {
        let key = line.trim_end_matches('\r').split('\t').next().unwrap_or("");
        !key.is_empty()
            && s.match_indices(key).any(|(pos, _)| {
                let left = &s[..pos];
                let right = &s[pos + key.len()..];
                let left_boundary = left.chars().last().is_some_and(boundary);
                let right_boundary = right.chars().next().is_some_and(boundary);
                ((left.is_empty() && right_boundary) || (left_boundary && (right.is_empty() || right_boundary)))
                    && window(left)
                    && window(right)
            })
    })
}

pub fn classify_keyword(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !keyword_pattern(s) {
        return;
    }
    let mut paths: Vec<Option<(i64, usize, AddressPart)>> = vec![None; s.len() + 1];
    paths[0] = Some((0, 0, AddressPart::Space));
    for (i, c) in s.char_indices() {
        let Some((base, _, _)) = paths[i] else { continue };
        let mut add = |to: usize, weight: i64, kind: AddressPart| {
            let value = base + weight;
            if paths[to].is_none_or(|(old, _, _)| value < old) {
                paths[to] = Some((value, i, kind));
            }
        };
        let next = i + c.len_utf8();
        if c == ' ' {
            add(next, 0, AddressPart::Space);
        }
        let comma = s[i..].trim_start_matches(is_nemo_space);
        if comma.starts_with(',') {
            add(s.len() - comma.len() + 1, 0, AddressPart::Comma);
        }
        if letter_word(c).is_some() {
            add(next, 0, AddressPart::Letter);
        }
        if matches!(c, '-' | '/') && symbol_word(c).is_some() {
            add(next, 0, AddressPart::Symbol);
        }
        let convertible =
            |ch: char| numbers::digit(ch).is_some() || letter_word(ch).is_some() || matches!(ch, '-' | '/');
        if !is_nemo_space(c) && c != ',' && !convertible(c) {
            for (offset, ch) in s[next..].char_indices() {
                if is_nemo_space(ch) || ch == ',' || convertible(ch) {
                    break;
                }
                add(next + offset + ch.len_utf8(), cost(0.1), AddressPart::Other);
            }
            add(next, cost(0.1), AddressPart::Other);
        }
        if letter_word(c).is_some() {
            for (offset, ch) in s[next..].char_indices() {
                if letter_word(ch).is_none() {
                    break;
                }
                add(next + offset + ch.len_utf8(), 0, AddressPart::Latin);
            }
        }
        if numbers::digit(c).is_some() {
            let mut count = 0;
            for (offset, ch) in s[i..].char_indices() {
                if numbers::digit(ch).is_none() {
                    break;
                }
                count += 1;
                let stop = i + offset + ch.len_utf8();
                let digits = &s[i..stop];
                if count >= 4 {
                    add(stop, cost(0.5), AddressPart::Number);
                } else if count == 1 || !numbers::is_zero(c) {
                    for (_, weight) in numbers::graph(digits) {
                        add(stop, cost(0.5) + weight, AddressPart::Number);
                    }
                }
            }
        }
        if numbers::digit(c).is_some() || super::serial::alpha(c) {
            for stop in s[next..].char_indices().map(|(j, _)| next + j).chain(std::iter::once(s.len())) {
                let piece = &s[i..stop];
                if piece.chars().any(is_nemo_space) || piece.contains(',') {
                    break;
                }
                if piece
                    .chars()
                    .all(|ch| numbers::digit(ch).is_some() || super::serial::alpha(ch) || matches!(ch, '-' | '/'))
                    && piece.chars().any(|ch| numbers::digit(ch).is_some())
                    && piece.chars().any(super::serial::alpha)
                {
                    let mut candidates = Vec::new();
                    super::serial::classify(piece, 0, &mut candidates);
                    for candidate in candidates {
                        add(stop, candidate.cost - cost(1.11), AddressPart::Serial);
                    }
                }
                if numbers::digit(c).is_some() {
                    let mut candidates = Vec::new();
                    super::numeric::classify(piece, 0, &mut candidates);
                    for candidate in candidates.into_iter().filter(|candidate| candidate.rank == 9) {
                        add(stop, candidate.cost - cost(1.1) - cost(0.0001), AddressPart::Ordinal);
                    }
                }
            }
        }
    }
    let Some((weight, _, _)) = paths[s.len()] else { return };
    out.push(Candidate {
        end,
        cost: cost(1.1) + weight,
        rank: 6,
        tag: super::lazy::tag(|| {
            let mut steps = Vec::new();
            let mut at = s.len();
            while at > 0 {
                let (_, from, kind) = paths[at].unwrap();
                steps.push((from, at, kind));
                at = from;
            }
            let mut text = String::new();
            for (from, to, kind) in steps.into_iter().rev() {
                let piece = &s[from..to];
                match kind {
                    AddressPart::Space => text.push(' '),
                    AddressPart::Comma => text.push(','),
                    AddressPart::Other | AddressPart::Latin => {
                        text.push(' ');
                        text.push_str(piece);
                    }
                    AddressPart::Letter => {
                        text.push(' ');
                        text.push_str(letter_word(piece.chars().next().unwrap()).unwrap());
                    }
                    AddressPart::Symbol => {
                        text.push(' ');
                        text.push_str(symbol_word(piece.chars().next().unwrap()).unwrap());
                    }
                    AddressPart::Number => {
                        text.push(' ');
                        text.push_str(&code_num(piece).unwrap_or_default());
                    }
                    AddressPart::Ordinal | AddressPart::Serial => {
                        let mut candidates = Vec::new();
                        if matches!(kind, AddressPart::Ordinal) {
                            super::numeric::classify(piece, 0, &mut candidates);
                        } else {
                            super::serial::classify(piece, 0, &mut candidates);
                        }
                        let rank = if matches!(kind, AddressPart::Ordinal) { 9 } else { 11 };
                        if let Some(candidate) =
                            candidates.into_iter().filter(|c| c.rank == rank).min_by_key(|c| c.cost)
                        {
                            let value = if rank == 9 {
                                candidate.tag.strip_prefix("ordinal { integer: \"").and_then(|s| s.strip_suffix("\" }"))
                            } else {
                                candidate.tag.strip_prefix("name: \"").and_then(|s| s.strip_suffix('"'))
                            };
                            if let Some(value) = value {
                                text.push(' ');
                                text.push_str(&value.replace('\u{a0}', " "));
                            }
                        }
                    }
                }
            }
            if text.starts_with(' ') {
                text.remove(0);
            }
            format!("measure {{ units: \"address\" cardinal {{ integer: \"{text}\" }} preserve_order: true }}")
        }),
    });
}
