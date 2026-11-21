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
use crate::lang::hu::taggers::ordinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};

pub const WEIGHT: Cost = cost(1.05);

fn parse_hm(text: &[char], start: usize) -> Option<(usize, u32, u32)> {
    if start >= text.len() || !text[start].is_ascii_digit() {
        return None;
    }
    let mut h_end = start;
    while h_end < text.len() && text[h_end].is_ascii_digit() && (h_end - start) <= 2 {
        h_end += 1;
    }
    if h_end >= text.len() || text[h_end] != ':' {
        return None;
    }
    let m_start = h_end + 1;
    let mut m_end = m_start;
    while m_end < text.len() && text[m_end].is_ascii_digit() && (m_end - m_start) <= 2 {
        m_end += 1;
    }
    if m_end - m_start != 2 {
        return None;
    }
    let h = text[start..h_end].iter().fold(0u32, |n, c| n * 10 + (*c as u32 - '0' as u32));
    let m = text[m_start..m_end].iter().fold(0u32, |n, c| n * 10 + (*c as u32 - '0' as u32));
    if h <= 24 && m < 60 { Some((m_end, h, m)) } else { None }
}

pub struct Composite;

impl Lazy for Composite {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }

        // 1. International decades: "1990s", "1990's", "2020s"
        if start + 4 <= text.len() && text[start].is_ascii_digit() {
            if let Some(year) = text[start..start + 4]
                .iter()
                .try_fold(0u64, |n, c| c.to_digit(10).filter(|_| c.is_ascii_digit()).map(|d| n * 10 + d as u64))
            {
                let cent = year / 100;
                let dec_digit = (year % 100) / 10;
                let unit_digit = year % 10;

                if (cent == 18 || cent == 19 || cent == 20) && unit_digit == 0 {
                    let mut p = start + 4;
                    let mut is_decade_suffix = false;
                    if p < text.len() && text[p] == 's' {
                        is_decade_suffix = true;
                        p += 1;
                    } else if p + 1 < text.len() && text[p] == '\'' && text[p + 1] == 's' {
                        is_decade_suffix = true;
                        p += 2;
                    }

                    if is_decade_suffix {
                        let end_pos = p;
                        let suffix = match dec_digit {
                            0 | 1 | 4 | 5 | 7 | 9 => "es",
                            _ => "as",
                        };
                        out.push(end_pos, WEIGHT, 0, move || {
                            let mut sp = String::new();
                            cardinal::spell(year, &mut sp);
                            let base_suffixed = format!("{sp}{suffix}");
                            let fixed = cardinal::apply_stem_fixes(&base_suffixed);
                            format!("name: \"{fixed} évek\"")
                        });
                        return;
                    }
                }
            }
        }

        // 2. Spaced ordinal before §: "12. §" -> "tizenkettedik paragrafus"
        let mut num_end = start;
        while num_end < text.len() && text[num_end].is_ascii_digit() {
            num_end += 1;
        }
        if num_end > start && num_end < text.len() {
            let mut dot_p = num_end;
            if text[dot_p] == ' ' {
                dot_p += 1;
            }
            if dot_p < text.len() && text[dot_p] == '.' {
                let mut space_p = dot_p + 1;
                if space_p < text.len() && text[space_p] == ' ' {
                    space_p += 1;
                    if space_p < text.len() && text[space_p] == '§' {
                        let end_pos = space_p + 1;
                        if let Some(n) = text[start..num_end]
                            .iter()
                            .try_fold(0u64, |n, c| n.checked_mul(10)?.checked_add(c.to_digit(10)? as u64))
                        {
                            out.push(end_pos, WEIGHT, 0, move || {
                                let mut sp = String::new();
                                cardinal::spell(n, &mut sp);
                                let ord = ordinal::cardinal_to_ordinal(&sp);
                                format!("name: \"{ord} paragrafus\"")
                            });
                            return;
                        }
                    }
                }
            }
        }

        // 3. Trailing English Eras: "500 BC", "500 AD", "500 BCE", "500 CE"
        if num_end > start && num_end < text.len() && text[num_end] == ' ' {
            let after_sp = num_end + 1;
            const ERAS: [(&str, &str); 7] = [
                ("B.C.E.", "időszámításunk előtt"),
                ("BCE", "időszámításunk előtt"),
                ("B.C.", "Krisztus előtt"),
                ("BC", "Krisztus előtt"),
                ("A.D.", "időszámításunk szerint"),
                ("AD", "időszámításunk szerint"),
                ("C.E.", "időszámításunk szerint"),
            ];

            for &(era_str, era_pref) in &ERAS {
                if text
                    .get(after_sp..after_sp + era_str.len())
                    .is_some_and(|part| part.iter().copied().eq(era_str.chars()))
                {
                    let end_pos = after_sp + era_str.len();
                    if let Some(y) = text[start..num_end]
                        .iter()
                        .try_fold(0u64, |n, c| n.checked_mul(10)?.checked_add(c.to_digit(10)? as u64))
                    {
                        out.push(end_pos, WEIGHT, 0, move || {
                            let mut sp = String::new();
                            cardinal::spell(y, &mut sp);
                            format!("name: \"{era_pref} {sp}\"")
                        });
                        return;
                    }
                }
            }
        }

        // 4. Time ranges: "9:00-17:00", "9:00–17:00" -> "kilenc órától tizenhét óráig"
        if let Some((t1_end, h1, m1)) = parse_hm(text, start) {
            let mut sep_p = t1_end;
            if sep_p < text.len() && text[sep_p] == ' ' {
                sep_p += 1;
            }
            if sep_p < text.len() && (text[sep_p] == '-' || text[sep_p] == '–' || text[sep_p] == '—') {
                let mut after_sep = sep_p + 1;
                if after_sep < text.len() && text[after_sep] == ' ' {
                    after_sep += 1;
                }
                if let Some((t2_end, h2, m2)) = parse_hm(text, after_sep) {
                    let end_pos = t2_end;
                    out.push(end_pos, WEIGHT, 0, move || {
                        let mut h1_sp = String::new();
                        cardinal::spell(h1 as u64, &mut h1_sp);
                        let s1 = if m1 == 0 {
                            format!("{h1_sp} órától")
                        } else {
                            let mut m1_sp = String::new();
                            cardinal::spell(m1 as u64, &mut m1_sp);
                            format!("{h1_sp} óra {m1_sp} perctől")
                        };

                        let mut h2_sp = String::new();
                        cardinal::spell(h2 as u64, &mut h2_sp);
                        let s2 = if m2 == 0 {
                            format!("{h2_sp} óráig")
                        } else {
                            let mut m2_sp = String::new();
                            cardinal::spell(m2 as u64, &mut m2_sp);
                            format!("{h2_sp} óra {m2_sp} percig")
                        };

                        format!("name: \"{s1} {s2}\"")
                    });
                }
            }
        }
    }
}
