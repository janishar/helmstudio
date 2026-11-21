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

use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashSet;
use std::sync::LazyLock;

static PROJECTED_INITIALS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    let mut initials = HashSet::new();
    for n in 1..1000 {
        let mut word = String::new();
        cardinal::spell_up_to_999(n, &mut word);
        if let Some(c) = word.chars().next() {
            initials.insert(c);
        }
    }
    for word in [
        "nulla",
        "első",
        "második",
        "nulladik",
        "ezer",
        "millió",
        "milliárd",
        "billió",
        "billiárd",
        "trillió",
        "trilliárd",
        "{word}",
    ] {
        if let Some(c) = word.chars().next() {
            initials.insert(c);
        }
    }
    for &(_, ending) in &ORDINAL_ENDINGS {
        if let Some(c) = ending.chars().next() {
            initials.insert(c);
        }
    }
    initials
});

// Recognize the output projection without enumerating the 24-digit input
// language: one minimum-weight witness per magnitude and output position.
fn projected_cardinal_cost(word: &str) -> Option<Cost> {
    const MAGNITUDES: [&str; 8] = ["", "ezer", "millió", "milliárd", "billió", "billiárd", "trillió", "trilliárd"];
    if matches!(word.as_bytes().first(), Some(b' ' | b'-')) || matches!(word.as_bytes().last(), Some(b' ' | b'-')) {
        return None;
    }
    if word == "nulla" {
        return Some(0);
    }
    let mut states = vec![(0usize, false, false, 0)];
    for group in (0..8).rev() {
        let mut next = Vec::new();
        for &(pos, written, thousand_one, weight) in &states {
            next.push((pos, written, false, weight));
            let may_separate = written && !(group == 0 && thousand_one);
            let separated = may_separate && matches!(word.as_bytes().get(pos), Some(b'-' | b' '));
            for begin in [Some(pos), separated.then_some(pos + 1)].into_iter().flatten() {
                if group != 0
                    && !word[begin..].contains(MAGNITUDES[group])
                    && !(group >= 2 && word[begin..].contains("{word}"))
                {
                    continue;
                }
                for n in 1..1000 {
                    let mut component = String::new();
                    if group == 0 || n != 1 {
                        cardinal::spell_up_to_999(n, &mut component);
                    }
                    component.push_str(MAGNITUDES[group]);
                    let mut variants = vec![component.clone()];
                    if group >= 2 && n == 1 {
                        variants.push("{word}".to_string());
                        variants.push("egy{word}".to_string());
                        variants.push("egy {word}".to_string());
                        variants.push(" egy{word}".to_string());
                        variants.push(" egy {word}".to_string());
                        variants.push(" {word}".to_string());
                    }
                    if group == 1 && n == 1 {
                        variants.push("egyezer".to_string());
                        variants.push("egy ezer".to_string());
                        variants.push(" egyezer".to_string());
                        variants.push(" egy ezer".to_string());
                        variants.push(" ezer".to_string());
                    }
                    if n / 100 == 1 {
                        if let Some(rest) = component.strip_prefix("száz") {
                            variants.push(format!("egyszáz{rest}"));
                            variants.push(format!("egy száz{rest}"));
                            variants.push(format!(" egyszáz{rest}"));
                            variants.push(format!(" egy száz{rest}"));
                            variants.push(format!(" száz{rest}"));
                        }
                    } else if n / 100 == 2 {
                        if let Some(rest) = component.strip_prefix("kétszáz") {
                            variants.push(format!("kettőszáz{rest}"));
                        }
                    }
                    if n % 100 == 2 && (n >= 100 || group == 0) {
                        let count = variants.len();
                        for index in 0..count {
                            if let Some(pos) = variants[index].rfind("kettő") {
                                variants.push(format!(
                                    "{}két{}",
                                    &variants[index][..pos],
                                    &variants[index][pos + "kettő".len()..]
                                ));
                            }
                        }
                    }
                    for variant in variants {
                        if word[begin..].starts_with(&variant) {
                            let end = begin + variant.len();
                            let one = group == 1 && n == 1;
                            let candidate = weight + if group >= 2 && n == 1 { -cost(0.001) } else { 0 };
                            if let Some(existing) = next.iter_mut().find(|s| s.0 == end && s.1 && s.2 == one) {
                                existing.3 = existing.3.min(candidate);
                            } else {
                                next.push((end, true, one, candidate));
                            }
                        }
                    }
                }
            }
        }
        states = next;
    }
    states.into_iter().filter(|s| s.0 == word.len() && s.1).map(|s| s.3).min()
}

pub const WEIGHT: Cost = cost(1.10);

const ORDINAL_ENDINGS: [(&str, &str); 23] = [
    ("trilliárd", "trilliárdodik"),
    ("trillió", "trilliomodik"),
    ("billiárd", "billiárdodik"),
    ("billió", "billiomodik"),
    ("milliárd", "milliárdodik"),
    ("millió", "milliomodik"),
    ("harminc", "harmincadik"),
    ("kilenc", "kilencedik"),
    ("nyolc", "nyolcadik"),
    ("három", "harmadik"),
    ("kettő", "kettedik"),
    ("száz", "századik"),
    ("ezer", "ezredik"),
    ("húsz", "huszadik"),
    ("négy", "negyedik"),
    ("két", "kettedik"),
    ("hét", "hetedik"),
    ("hat", "hatodik"),
    ("tíz", "tizedik"),
    ("egy", "egyedik"),
    ("ven", "venedik"),
    ("van", "vanadik"),
    ("öt", "ötödik"),
];

pub fn cardinal_to_ordinal(card: &str) -> String {
    // Whole-word exceptions
    match card {
        "egy" => return "első".to_string(),
        "kettő" | "két" => return "második".to_string(),
        "nulla" => return "nulladik".to_string(),
        _ => {}
    }

    for &(pat, rep) in &ORDINAL_ENDINGS {
        if card.ends_with(pat) {
            let prefix = &card[..card.len() - pat.len()];
            return format!("{prefix}{rep}");
        }
    }

    card.to_string()
}

pub fn cardinal_to_fractional(card: &str) -> String {
    let ord = cardinal_to_ordinal(card);
    match ord.as_str() {
        "első" => "egyed".to_string(),
        "második" => "fél".to_string(),
        other => {
            if let Some(stripped) = other.strip_suffix("ik") {
                stripped.to_string()
            } else {
                other.to_string()
            }
        }
    }
}

pub fn cardinal_to_superessive(card: &str) -> String {
    let ord = cardinal_to_ordinal(card);
    if ord == "első" {
        return "elsőn".to_string();
    }
    if let Some(stripped) = ord.strip_suffix("odik") {
        return format!("{stripped}odikon");
    }
    if let Some(stripped) = ord.strip_suffix("edik") {
        return format!("{stripped}ediken");
    }
    if let Some(stripped) = ord.strip_suffix("adik") {
        return format!("{stripped}adikon");
    }
    if let Some(stripped) = ord.strip_suffix("ödik") {
        return format!("{stripped}ödiken");
    }
    ord
}

pub struct Ordinal;

impl Lazy for Ordinal {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        for (end, n) in cardinal::numbers(text, start) {
            if text.get(end) == Some(&'.') {
                out.push(end + 1, WEIGHT + cardinal::number_cost(n), 0, || {
                    let mut word = String::new();
                    cardinal::spell_number(n, &mut word);
                    let ordinal = cardinal_to_ordinal(&word);
                    format!("ordinal {{ integer: \"{ordinal}\" }}")
                });
            }
        }
        let Some(&first) = text.get(start) else { return };
        if first.is_ascii_digit() || !PROJECTED_INITIALS.contains(&first) {
            return;
        }
        let mut end = start;
        while end < text.len()
            && (crate::lang::hu::graph_utils::is_hu_alpha(text[end]) || matches!(text[end], '-' | ' ' | '{' | '}'))
        {
            end += 1;
            // Every ordinal ending ends in 'k', except the whole-word
            // exception "első"; the projected literal may also end in '}'.
            if !matches!(text[end - 1], 'k' | 'ő' | '}') {
                continue;
            }
            let part = &text[start..end];
            let suffix_matches = |suffix: &str| {
                let mut chars = part.iter().rev().copied();
                suffix.chars().rev().all(|c| chars.next() == Some(c))
            };
            if !part.ends_with(&['}'])
                && !["első", "második", "nulladik"].iter().any(|s| suffix_matches(s))
                && !ORDINAL_ENDINGS.iter().any(|(_, s)| suffix_matches(s))
            {
                continue;
            }
            let output: String = part.iter().collect();
            let mut best = None;
            if output.ends_with('}') {
                best = projected_cardinal_cost(&output);
            }
            for (card, ord) in [("egy", "első"), ("kettő", "második"), ("nulla", "nulladik")] {
                if output == ord {
                    if let Some(weight) = projected_cardinal_cost(card) {
                        best = Some(best.map_or(weight, |current: Cost| current.min(weight)));
                    }
                }
            }
            for &(card_ending, ordinal_ending) in &ORDINAL_ENDINGS {
                if let Some(prefix) = output.strip_suffix(ordinal_ending) {
                    let card = format!("{prefix}{card_ending}");
                    if cardinal_to_ordinal(&card) == output {
                        if let Some(weight) = projected_cardinal_cost(&card) {
                            best = Some(best.map_or(weight, |current: Cost| current.min(weight)));
                        }
                    }
                }
            }
            if let Some(weight) = best {
                out.push(end, WEIGHT + weight, 0, || format!("ordinal {{ integer: \"{output}\" }}"));
            }
        }
    }
}
