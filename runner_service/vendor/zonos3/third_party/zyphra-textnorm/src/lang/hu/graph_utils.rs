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

use std::collections::HashMap;
use std::sync::LazyLock;

pub const ALPHA_UPPER: &str = "AÁBCDEÉFGHIÍJKLMNOÓÖŐPQRSTUÚÜŰVWXYZ";
pub const ALPHA_LOWER: &str = "aábcdeéfghiíjklmnoóöőpqrstuúüűvwxyz";
pub const VOWELS: &str = "AÁEÉIÍOÓÖŐUÚÜŰaáeéiíoóöőuúüű";

#[inline]
pub fn is_hu_upper(c: char) -> bool {
    c.is_ascii_uppercase() || matches!(c, 'Á' | 'É' | 'Í' | 'Ó' | 'Ö' | 'Ő' | 'Ú' | 'Ü' | 'Ű')
}

#[inline]
pub fn is_hu_lower(c: char) -> bool {
    c.is_ascii_lowercase() || matches!(c, 'á' | 'é' | 'í' | 'ó' | 'ö' | 'ő' | 'ú' | 'ü' | 'ű')
}

#[inline]
pub fn is_hu_alpha(c: char) -> bool {
    is_hu_upper(c) || is_hu_lower(c)
}

#[inline]
pub fn is_hu_vowel(c: char) -> bool {
    VOWELS.contains(c)
}

pub fn to_hu_lower(c: char) -> char {
    match c {
        'A' => 'a',
        'Á' => 'á',
        'B' => 'b',
        'C' => 'c',
        'D' => 'd',
        'E' => 'e',
        'É' => 'é',
        'F' => 'f',
        'G' => 'g',
        'H' => 'h',
        'I' => 'i',
        'Í' => 'í',
        'J' => 'j',
        'K' => 'k',
        'L' => 'l',
        'M' => 'm',
        'N' => 'n',
        'O' => 'o',
        'Ó' => 'ó',
        'Ö' => 'ö',
        'Ő' => 'ő',
        'P' => 'p',
        'Q' => 'q',
        'R' => 'r',
        'S' => 's',
        'T' => 't',
        'U' => 'u',
        'Ú' => 'ú',
        'Ü' => 'ü',
        'Ű' => 'ű',
        'V' => 'v',
        'W' => 'w',
        'X' => 'x',
        'Y' => 'y',
        'Z' => 'z',
        other => other,
    }
}

pub fn to_hu_upper(c: char) -> char {
    match c {
        'a' => 'A',
        'á' => 'Á',
        'b' => 'B',
        'c' => 'C',
        'd' => 'D',
        'e' => 'E',
        'é' => 'É',
        'f' => 'F',
        'g' => 'G',
        'h' => 'H',
        'i' => 'I',
        'í' => 'Í',
        'j' => 'J',
        'k' => 'K',
        'l' => 'L',
        'm' => 'M',
        'n' => 'N',
        'o' => 'O',
        'ó' => 'Ó',
        'ö' => 'Ö',
        'ő' => 'Ő',
        'p' => 'P',
        'q' => 'Q',
        'r' => 'R',
        's' => 'S',
        't' => 'T',
        'u' => 'U',
        'ú' => 'Ú',
        'ü' => 'Ü',
        'ű' => 'Ű',
        'v' => 'V',
        'w' => 'W',
        'x' => 'X',
        'y' => 'Y',
        'z' => 'Z',
        other => other,
    }
}

pub fn hu_to_lower_str(s: &str) -> String {
    s.chars().map(to_hu_lower).collect()
}

pub fn hu_to_upper_str(s: &str) -> String {
    s.chars().map(to_hu_upper).collect()
}

pub fn hu_capitalize(s: &str) -> String {
    let mut chars = s.chars();
    match chars.next() {
        None => String::new(),
        Some(first) => {
            let mut res = String::with_capacity(s.len());
            res.push(to_hu_upper(first));
            for c in chars {
                res.push(to_hu_lower(c));
            }
            res
        }
    }
}

// Inflection tables
struct InflectionData {
    singular: HashMap<String, Vec<String>>,
    plural: HashMap<String, Vec<String>>,
    lexical: HashMap<String, Vec<String>>,
    keys_sorted: Vec<String>,
}

fn parse_inflection_tsv(tsv_content: &str) -> HashMap<String, Vec<String>> {
    let mut map = HashMap::new();
    for line in tsv_content.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if let Some((k, v)) = line.split_once('\t') {
            let endings: Vec<String> = v.split_whitespace().map(|s| s.to_string()).collect();
            map.insert(k.to_string(), endings);
        }
    }
    map
}

static INFLECTION: LazyLock<InflectionData> = LazyLock::new(|| {
    let singular =
        parse_inflection_tsv(include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/inflection/endings.tsv")));
    let plural = parse_inflection_tsv(include_str!(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/data/hu/inflection/plural_endings.tsv"
    )));
    let lexical =
        parse_inflection_tsv(include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/inflection/word_endings.tsv")));
    let mut keys_sorted: Vec<String> = singular.keys().cloned().collect();
    keys_sorted.sort_by(|a, b| b.len().cmp(&a.len()));
    InflectionData { singular, plural, lexical, keys_sorted }
});

fn modify_ending(outword: &str, word: &str, form: &str) -> String {
    if outword == word {
        return form.to_string();
    }
    const ENDINGS: [&str; 13] = ["e-", "ny", "nny", "ly", "lly", "ev", "év", "út", "ut", "a", "á", "e", "é"];
    for &ending in &ENDINGS {
        if form.starts_with(ending) {
            let final_prefix = match ending {
                "nny" => "ny",
                "lly" => "ly",
                _ => "",
            };
            return format!("{}{}", final_prefix, &form[ending.len()..]);
        }
    }
    form.to_string()
}

pub fn naive_inflector(abbr: &str, word: &str, singular_only: bool) -> Vec<(String, String)> {
    let data = &*INFLECTION;
    let (key, ends): (&str, &[String]) = if let Some(ends) = data.lexical.get(word) {
        (word, ends.as_slice())
    } else {
        let mut found = None;
        for k in &data.keys_sorted {
            if word.ends_with(k) {
                if let Some(ends) = data.singular.get(k) {
                    found = Some((k.as_str(), ends.as_slice()));
                    break;
                }
            }
        }
        match found {
            Some(pair) => pair,
            None => return Vec::new(),
        }
    };

    let mut outword = word.to_string();
    for &wordend in &["ny", "ly", "év", "út", "a", "e"] {
        if outword.ends_with(wordend) {
            outword.truncate(outword.len() - wordend.len());
        }
    }

    let tweak = |form: &str| modify_ending(&outword, word, form);

    let (abbr_base, has_dash) = match abbr.split_once('-') {
        Some((b, _)) => (b, true),
        None => (abbr, false),
    };

    let mut forms = Vec::new();
    for form in ends {
        let tw = tweak(form);
        forms.push((format!("{abbr_base}-{tw}"), format!("{outword}{form}")));
        if has_dash {
            forms.push((format!("{abbr}{tw}"), format!("{outword}{form}")));
        }
    }

    if !singular_only {
        if let Some(plural_list) = data.plural.get(key) {
            for plural_form in plural_list {
                let plural_key = if plural_form == "k" { format!("{key}k") } else { plural_form.clone() };
                let tw_pl = tweak(plural_form);
                forms.push((format!("{abbr_base}-{tw_pl}"), format!("{outword}{plural_form}")));
                if has_dash {
                    forms.push((format!("{abbr}{tw_pl}"), format!("{outword}{plural_form}")));
                }
                if let Some(sing_endings) = data.singular.get(&plural_key) {
                    for form in sing_endings {
                        forms.push((format!("{abbr_base}-{tw_pl}{form}"), format!("{outword}{plural_form}{form}")));
                        if has_dash {
                            forms.push((format!("{abbr}{tw_pl}{form}"), format!("{outword}{plural_form}{form}")));
                        }
                    }
                }
            }
        }
    }

    forms
}

pub fn inflect_abbreviation(abbr: &str, word: &str, singular_only: bool) -> Vec<(String, String)> {
    let abbr_lower = abbr.to_lowercase();
    let last = match abbr_lower.chars().last() {
        Some(c) => c,
        None => return Vec::new(),
    };
    let ending = if "bcdgjptvz".contains(last) {
        "é".to_string()
    } else if "aáeéiíoóöőuúüű".contains(last) {
        last.to_string()
    } else if "flmnrs".contains(last) {
        format!("e{last}")
    } else if "hk".contains(last) {
        "á".to_string()
    } else {
        return Vec::new();
    };

    let word_part = naive_inflector(".", word, singular_only);
    let abbr_part = naive_inflector(abbr, &ending, singular_only);

    let mut result = Vec::new();
    for (a, w) in abbr_part.into_iter().zip(word_part.into_iter()) {
        result.push((a.0, w.1));
    }
    result
}
