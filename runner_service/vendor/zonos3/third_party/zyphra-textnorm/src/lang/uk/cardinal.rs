// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Number morphology from uk/taggers/cardinal.py.

#[derive(Clone, Copy, PartialEq, Eq)]
pub(super) enum Gender {
    Masculine,
    Feminine,
}

const DIGITS: [&str; 10] = ["нуль", "один", "два", "три", "чотири", "п'ять", "шість", "сім", "вісім", "дев'ять"];
const TEENS: [&str; 10] = [
    "десять",
    "одинадцять",
    "дванадцять",
    "тринадцять",
    "чотирнадцять",
    "п'ятнадцять",
    "шістнадцять",
    "сімнадцять",
    "вісімнадцять",
    "дев'ятнадцять",
];
const TENS: [&str; 10] =
    ["", "", "двадцять", "тридцять", "сорок", "п'ятдесят", "шістдесят", "сімдесят", "вісімдесят", "дев'яносто"];
const HUNDREDS: [&str; 10] =
    ["", "сто", "двісті", "триста", "чотириста", "п'ятсот", "шістсот", "сімсот", "вісімсот", "дев'ятсот"];
const GEN_DIGITS: [&str; 10] =
    ["нуля", "одного", "двох", "трьох", "чотирьох", "п'яти", "шести", "семи", "восьми", "дев'яти"];
const GEN_TEENS: [&str; 10] = [
    "десяти",
    "одинадцяти",
    "дванадцяти",
    "тринадцяти",
    "чотирнадцяти",
    "п'ятнадцяти",
    "шістнадцяти",
    "сімнадцяти",
    "вісімнадцяти",
    "дев'ятнадцяти",
];
const GEN_TENS: [&str; 10] =
    ["", "", "двадцяти", "тридцяти", "сорока", "п'ятдесяти", "шістдесяти", "сімдесяти", "вісімдесяти", "дев'яноста"];
const GEN_HUNDREDS: [&str; 10] =
    ["", "ста", "двохсот", "трьохсот", "чотирьохсот", "п'ятисот", "шестисот", "семисот", "восьмисот", "дев'ятисот"];

pub(super) fn digit(n: usize) -> &'static str {
    DIGITS[n]
}

fn group(n: usize, gender: Gender, genitive: bool, out: &mut [&'static str; 4], len: &mut usize) {
    let mut push = |word| {
        out[*len] = word;
        *len += 1;
    };
    let h = n / 100;
    let t = n / 10 % 10;
    let u = n % 10;
    if h != 0 {
        push(if genitive { GEN_HUNDREDS[h] } else { HUNDREDS[h] });
    }
    if t == 1 {
        push(if genitive { GEN_TEENS[u] } else { TEENS[u] });
    } else {
        if t != 0 {
            push(if genitive { GEN_TENS[t] } else { TENS[t] });
        }
        if u != 0 {
            push(if genitive {
                if u == 1 && gender == Gender::Feminine { "однієї" } else { GEN_DIGITS[u] }
            } else {
                match (u, gender) {
                    (1, Gender::Feminine) => "одна",
                    (2, Gender::Feminine) => "дві",
                    _ => DIGITS[u],
                }
            });
        }
    }
}

/// The source graph accepts ungrouped ASCII numbers through 15 digits, without leading zeroes.
#[inline]
pub(super) fn valid_number(input: &str) -> bool {
    !input.is_empty()
        && input.len() <= 15
        && input.bytes().all(|b| b.is_ascii_digit())
        && (input.len() == 1 || !input.starts_with('0'))
}

pub(super) fn number(input: &str, gender: Gender, genitive: bool) -> Option<String> {
    if !valid_number(input) {
        return None;
    }
    let n: u64 = input.parse().ok()?;
    if n == 0 {
        return Some(if genitive { "нуля" } else { "нуль" }.to_owned());
    }
    let scales = if genitive {
        [
            ["", "", ""],
            ["тисячі", "тисяч", "тисяч"],
            ["мільйона", "мільйонів", "мільйонів"],
            ["мільярда", "мільярдів", "мільярдів"],
            ["трильйона", "трильйонів", "трильйонів"],
        ]
    } else {
        [
            ["", "", ""],
            ["тисяча", "тисячі", "тисяч"],
            ["мільйон", "мільйони", "мільйонів"],
            ["мільярд", "мільярди", "мільярдів"],
            ["трильйон", "трильйони", "трильйонів"],
        ]
    };
    let mut groups = [0usize; 5];
    let mut rest = n;
    for item in &mut groups {
        *item = (rest % 1000) as usize;
        rest /= 1000;
    }
    let mut result = String::with_capacity(input.len() * 10);
    let mut pending_spaces = 0usize;
    for i in (0..5).rev() {
        let v = groups[i];
        if v != 0 {
            let mut words = [""; 4];
            let mut len = 0;
            group(
                v,
                if i == 0 {
                    gender
                } else if i == 1 {
                    Gender::Feminine
                } else {
                    Gender::Masculine
                },
                genitive,
                &mut words,
                &mut len,
            );
            if i != 0 {
                let form = if v % 100 / 10 == 1 {
                    2
                } else {
                    match v % 10 {
                        1 => 0,
                        2..=4 => 1,
                        _ => 2,
                    }
                };
                words[len] = scales[i][form];
                len += 1;
            } else {
                pending_spaces += 1;
            }
            for &word in &words[..len] {
                if !result.is_empty() {
                    for _ in 0..(pending_spaces + 1) / 2 {
                        result.push(' ');
                    }
                }
                result.push_str(word);
                pending_spaces = 1;
            }
            // The final internal separator is not part of the group graph.
            pending_spaces -= 1;
        }
        if i != 0 {
            pending_spaces += 1;
        }
    }
    Some(result)
}

pub(super) fn graph(s: &str, gender: Gender, genitive: bool) -> Option<String> {
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return number(s, gender, genitive);
    }
    let groups: Vec<_> = s.split([' ', '\u{a0}', '.']).collect();
    if groups.len() < 2
        || groups.len() > 5
        || groups[0].is_empty()
        || groups[0].len() > 3
        || groups[0].starts_with('0')
        || groups.iter().skip(1).any(|x| x.len() != 3)
        || groups.iter().any(|x| !x.bytes().all(|b| b.is_ascii_digit()))
    {
        return None;
    }
    number(&groups.concat(), gender, genitive)
}

pub(super) fn basic(s: &str) -> Option<(String, i32)> {
    let (prefix, s, weight) = if let Some(s) = s.strip_prefix('~') {
        ("приблизно ", s.strip_prefix(' ').unwrap_or(s), -10)
    } else if let Some(s) = s.strip_prefix('±') {
        ("плюс-мінус ", s.strip_prefix(' ').unwrap_or(s), -10)
    } else {
        ("", s, 10)
    };
    let (minus, s) =
        if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("мінус ", s) } else { ("", s) };
    let value = graph(s, Gender::Masculine, false).or_else(|| {
        if s.starts_with('0') {
            let stripped = s.trim_start_matches('0');
            if !stripped.is_empty() {
                return graph(stripped, Gender::Masculine, false);
            }
        }
        if s.len() >= 16 && s.bytes().all(|b| b.is_ascii_digit()) {
            return Some(s.bytes().map(|b| digit((b - b'0') as usize)).collect::<Vec<_>>().join(" "));
        }
        None
    })?;
    Some((format!("{prefix}{minus}{value}"), weight))
}

pub(super) fn classify(s: &str) -> Vec<(String, i32)> {
    let mut out = Vec::new();
    if let Some(v) = basic(s) {
        out.push(v);
    }
    let (modifier, raw) = if let Some(v) = s.strip_prefix('~') {
        ("приблизно ", v.strip_prefix(' ').unwrap_or(v))
    } else if let Some(v) = s.strip_prefix('±') {
        ("плюс-мінус ", v.strip_prefix(' ').unwrap_or(v))
    } else {
        ("", s)
    };
    let (minus, raw) = if let Some(v) = raw.strip_prefix('-').or_else(|| raw.strip_prefix('−')) {
        ("мінус ", v)
    } else {
        ("", raw)
    };
    if raw.len() >= 16 && raw.bytes().all(|b| b.is_ascii_digit()) {
        let w = raw.bytes().map(|b| digit((b - b'0') as usize)).collect::<Vec<_>>().join(" ");
        out.push((format!("{modifier}{minus}{w}"), if modifier.is_empty() { 10 } else { -10 }));
    }
    for dash in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(dash) {
            if let (Some(a), Some(b)) = (graph(a, Gender::Masculine, true), graph(b, Gender::Masculine, true)) {
                out.push((format!("від {a} до {b}"), 20));
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if let (Some(a), Some(b)) = (
            graph(a, Gender::Masculine, false),
            if b == "1" { Some("одного".to_owned()) } else { graph(b, Gender::Masculine, false) },
        ) {
            out.push((format!("{a} до {b}"), 20));
        }
    }
    let list: Vec<_> = s.split(',').collect();
    if list.len() >= 3 && list.iter().all(|x| !x.is_empty() && x.len() <= 3 && x.bytes().all(|b| b.is_ascii_digit())) {
        if let Some(items) = list.iter().map(|x| graph(x, Gender::Masculine, false)).collect::<Option<Vec<_>>>() {
            out.push((items.join(", "), 15));
        }
    }
    for (suffix, forms, gender) in [
        ("млн.", ["мільйон", "мільйони", "мільйонів"], Gender::Masculine),
        ("млн", ["мільйон", "мільйони", "мільйонів"], Gender::Masculine),
        ("тис.", ["тисяча", "тисячі", "тисяч"], Gender::Feminine),
        ("тис", ["тисяча", "тисячі", "тисяч"], Gender::Feminine),
        ("млрд.", ["мільярд", "мільярди", "мільярдів"], Gender::Masculine),
        ("млрд", ["мільярд", "мільярди", "мільярдів"], Gender::Masculine),
        ("трлн.", ["трильйон", "трильйони", "трильйонів"], Gender::Masculine),
        ("трлн", ["трильйон", "трильйони", "трильйонів"], Gender::Masculine),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            let b = n.as_bytes();
            if let (Some(&last), Some(v)) = (b.last(), number(n, gender, false)) {
                let form = if b.len() > 1 && b[b.len() - 2] == b'1' {
                    2
                } else {
                    match last {
                        b'1' => 0,
                        b'2'..=b'4' => 1,
                        _ => 2,
                    }
                };
                out.push((format!("{v} {}", forms[form]), 5));
            }
        }
    }
    if let Some((base, power)) = s
        .split_once('^')
        .or_else(|| s.strip_suffix('²').map(|x| (x, "2")))
        .or_else(|| s.strip_suffix('³').map(|x| (x, "3")))
    {
        if base.chars().count() == 1 {
            let c = base.chars().next().unwrap();
            let v = match c {
                'a' | 'A' => Some("а"),
                'b' | 'B' => Some("б"),
                'c' | 'C' => Some("це"),
                'x' | 'X' => Some("ікс"),
                'y' | 'Y' => Some("ігрек"),
                'z' | 'Z' => Some("зед"),
                _ => super::electronic::letter(c),
            };
            let v = v.map(str::to_owned).or_else(|| {
                if "абвгґдеєжзиіїйклмнопрстуфхцчшщьюяАБВГҐДЕЄЖЗИІЇЙКЛМНОПРСТУФХЦЧШЩЬЮЯ'ʼ’".contains(c)
                {
                    Some(c.to_string())
                } else {
                    None
                }
            });
            if let Some(v) = v {
                let suffix = match power {
                    "2" => Some("квадрат".to_owned()),
                    "3" => Some("куб".to_owned()),
                    _ => {
                        let (m, p) = if let Some(p) = power.strip_prefix('-').or_else(|| power.strip_prefix('−')) {
                            ("мінус ", p)
                        } else {
                            ("", power)
                        };
                        if p.len() <= 4 && p != "2" && p != "3" {
                            number(p, Gender::Masculine, false).map(|p| format!("в степені {m}{p}"))
                        } else {
                            None
                        }
                    }
                };
                if let Some(p) = suffix {
                    out.push((format!("{v} {p}"), 100));
                    if let Some(latin) = super::electronic::letter(c) {
                        if latin != v {
                            out.push((format!("{latin} {p}"), 100));
                        }
                    }
                }
            }
        }
    }
    for (k, v) in
        [("CO2", "сі о два"), ("AB-123-CD", "е й б і сто двадцять три с і д і"), ("2-б-34", "два б три чотири")]
    {
        if s == k {
            out.push((v.to_owned(), 5));
        }
    }
    let version = s.strip_prefix('v').or_else(|| s.strip_prefix('V'));
    if let Some(v) = version {
        if let Some((a, b)) = v.split_once('.') {
            if a.len() == 1 && b.len() == 1 {
                if let (Some(a), Some(b)) = (number(a, Gender::Masculine, false), number(b, Gender::Masculine, false)) {
                    out.push((format!("ві {a} крапка {b}"), 5));
                }
            }
        }
    }
    let dotted: Vec<_> = s.split('.').collect();
    if (2..=5).contains(&dotted.len()) && dotted.iter().all(|x| x.len() == 1 && x.bytes().all(|b| b.is_ascii_digit())) {
        if let Some(v) = dotted.iter().map(|x| number(x, Gender::Masculine, false)).collect::<Option<Vec<_>>>() {
            out.push((v.join(" крапка "), 5));
        }
    }
    let split = s.find(|c: char| !c.is_ascii_alphabetic()).unwrap_or(s.len());
    if split > 0 {
        let (letters, rest) = s.split_at(split);
        let n = rest.strip_prefix('-').unwrap_or(rest);
        if (1..=6).contains(&n.len()) {
            if let Some(value) = number(n, Gender::Masculine, false) {
                let names = letters.chars().filter_map(super::electronic::letter).collect::<Vec<_>>().join(" ");
                out.push((format!("{names} {value}"), 5));
            }
        }
    }
    if s.chars().any(|c| c.is_ascii_digit()) {
        let mut chunks = Vec::new();
        let mut current = String::new();
        let mut numeric = None;
        let mut valid = true;
        let mut separators = Vec::new();
        for c in s.chars() {
            let d = c.is_ascii_digit();
            if matches!(c, '.' | '-' | '_' | '/') {
                if current.is_empty() {
                    valid = false;
                    break;
                }
                chunks.push((std::mem::take(&mut current), numeric.unwrap()));
                separators.push(Some(c));
                numeric = None;
            } else if d
                || super::electronic::letter(c).is_some()
                || "абвгґдеєжзиіїйклмнопрстуфхцчшщьюяАБВГҐДЕЄЖЗИІЇЙКЛМНОПРСТУФХЦЧШЩЬЮЯ'ʼ’".contains(c)
            {
                if let Some(prev) = numeric {
                    if prev != d {
                        chunks.push((std::mem::take(&mut current), prev));
                        separators.push(None);
                    }
                }
                current.push(c);
                numeric = Some(d);
            } else {
                valid = false;
                break;
            }
        }
        if valid && !current.is_empty() {
            chunks.push((current, numeric.unwrap()));
            let mixed_separators = separators.iter().any(Option::is_none) && separators.iter().any(Option::is_some);
            if chunks.len() >= 2 && !mixed_separators {
                let mut words = String::new();
                for (i, (chunk, is_num)) in chunks.iter().enumerate() {
                    if i > 0 {
                        words.push_str(if separators[i - 1] == Some('.') { " крапка " } else { " " });
                    }
                    if *is_num {
                        let n = graph(chunk, Gender::Masculine, false).or_else(|| {
                            let s = chunk.trim_start_matches('0');
                            if s.is_empty() { None } else { graph(s, Gender::Masculine, false) }
                        });
                        if let Some(n) = n {
                            words.push_str(&n)
                        } else {
                            valid = false;
                            break;
                        }
                    } else {
                        for c in chunk.chars() {
                            if let Some(w) = super::electronic::letter(c) { words.push_str(w) } else { words.push(c) }
                        }
                    }
                }
                if valid {
                    out.push((words, 150));
                }
            }
        }
    }
    out
}
