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

use super::s;
use crate::tag::{Field, Value};

crate::tsv!(SYMBOLS, "hu/electronic/symbols.tsv");
crate::tsv!(DOMAIN, "hu/electronic/domain.tsv");
crate::tsv!(SERVER, "hu/electronic/server_name.tsv");

fn digit_word(c: char) -> Option<&'static str> {
    match c {
        '0' => Some("nulla"),
        '1' => Some("egy"),
        '2' => Some("kettő"),
        '3' => Some("három"),
        '4' => Some("négy"),
        '5' => Some("öt"),
        '6' => Some("hat"),
        '7' => Some("hét"),
        '8' => Some("nyolc"),
        '9' => Some("kilenc"),
        _ => None,
    }
}

fn symbol_word(c: char) -> Option<&'static str> {
    let mut key = [0; 4];
    SYMBOLS.exact(c.encode_utf8(&mut key)).next().map(|row| row.value.as_str())
}

fn verbalize_chars(s: &str) -> String {
    let mut words: Vec<String> = Vec::new();
    for c in s.chars() {
        if let Some(dw) = digit_word(c) {
            words.push(dw.to_string());
        } else if let Some(sw) = symbol_word(c) {
            words.push(sw.to_string());
        } else {
            words.push(c.to_string());
        }
    }
    words.join(" ")
}

fn verbalize_domain(s: &str) -> String {
    let mut best: Vec<Option<(usize, String)>> = vec![None; s.len() + 1];
    best[s.len()] = Some((0, String::new()));
    for (i, c) in s.char_indices().rev() {
        let next = i + c.len_utf8();
        let (score, suffix) = best[next].as_ref().unwrap();
        let mut choice = (score + 1, verbalize_chars(&s[i..next]));
        if !suffix.is_empty() {
            choice.1.push(' ');
            choice.1.push_str(suffix);
        }
        for row in DOMAIN.rows().iter().chain(SERVER.rows().iter()) {
            if row.key.is_empty() || !s[i..].starts_with(&row.key) {
                continue;
            }
            let end = i + row.key.len();
            if let Some((cost, rest)) = &best[end] {
                if *cost < choice.0 {
                    let mut value = String::new();
                    for c in row.value.chars() {
                        if let Some(word) = digit_word(c).or_else(|| symbol_word(c)) {
                            value.push_str(word);
                        } else {
                            value.push(c);
                        }
                    }
                    if !rest.is_empty() {
                        value.push(' ');
                        value.push_str(rest);
                    }
                    choice = (*cost, value);
                }
            }
        }
        best[i] = Some(choice);
    }
    best[0].take().unwrap().1
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    let result = if let Some(user) = s(d, i, "username") {
        if user.is_empty() || user.contains([' ', '"']) {
            return None;
        }
        i += 1;
        if let Some(dom) = s(d, i, "domain") {
            if dom.is_empty() || dom.contains('"') {
                return None;
            }
            i += 1;
            format!("{} kukac {}", verbalize_chars(user), verbalize_domain(dom))
        } else {
            format!("kukac {}", verbalize_chars(user))
        }
    } else {
        let protocol = s(d, i, "protocol");
        if protocol.is_some() {
            i += 1;
        }
        let dom = s(d, i, "domain")?;
        if dom.is_empty() || dom.contains('"') || protocol.is_some_and(|p| p.is_empty() || p.contains([' ', '"'])) {
            return None;
        }
        i += 1;
        match protocol {
            Some(p) => {
                let protocol = p
                    .chars()
                    .map(|c| symbol_word(c).map(str::to_string).unwrap_or_else(|| c.to_string()))
                    .collect::<Vec<_>>()
                    .join(" ");
                format!("{} {}", protocol, verbalize_domain(dom))
            }
            None => verbalize_domain(dom),
        }
    };
    if !d[i..].iter().all(|(key, value)| match (key.as_ref(), value) {
        ("preserve_order", Value::True) => true,
        ("field_order", Value::Str(value)) => value.chars().count() == 1 && !value.contains('"'),
        _ => false,
    }) {
        return None;
    }
    Some(result)
}
