//! Gujarati measurements and fractional unit readings.
use super::{numbers, numeric};
use crate::lattice::Candidate;
use crate::text::cost;
const UNIT: &str = include_str!("../../../data/gu/measure/unit.tsv");
const FORMAL: &str = include_str!("../../../data/gu/measure/unit_year_formal.tsv");
const QUARTER1: &str = include_str!("../../../data/gu/measure/quarterly_units_map.tsv");
const QUARTER2: &str = include_str!("../../../data/gu/measure/quarterly_units_list.tsv");
const PAUNE: &str = include_str!("../../../data/gu/whitelist/paune_mappings.tsv");
pub(crate) fn number_char(c: char) -> bool {
    if numeric::structural_char(c) || matches!(c, '—' | 'x' | 'X' | '*' | '\t' | '\n' | '\r' | '\u{a0}') {
        return true;
    }
    struct Alphabet {
        ascii: [bool; 128],
        gujarati: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static ALPHABET: std::sync::LazyLock<Alphabet> = std::sync::LazyLock::new(|| {
        let mut a = Alphabet { ascii: [false; 128], gujarati: [false; 128], other: Default::default() };
        let keys = rows(UNIT)
            .chain(rows(FORMAL))
            .chain(rows(QUARTER1))
            .chain(rows(include_str!("../../../data/gu/numbers/thousands.tsv")))
            .map(|(key, _)| key)
            .chain(QUARTER2.lines().map(|line| line.trim_end_matches('\r')));
        for c in keys.flat_map(str::chars) {
            let n = c as usize;
            if n < 128 {
                a.ascii[n] = true;
            } else if (0x0a80..=0x0aff).contains(&n) {
                a.gujarati[n - 0x0a80] = true;
            } else {
                a.other.insert(c);
            }
        }
        a
    });
    let n = c as usize;
    if n < 128 {
        ALPHABET.ascii[n]
    } else if (0x0a80..=0x0aff).contains(&n) {
        ALPHABET.gujarati[n - 0x0a80]
    } else {
        ALPHABET.other.contains(&c)
    }
}
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
type UnitRow = (&'static str, &'static str);
struct UnitIndex {
    ascii: [Vec<UnitRow>; 128],
    gujarati: [Vec<UnitRow>; 128],
    other: std::collections::HashMap<char, Vec<UnitRow>>,
}
impl UnitIndex {
    fn get(&self, c: char) -> &[UnitRow] {
        let n = c as usize;
        if n < 128 {
            &self.ascii[n]
        } else if (0x0a80..=0x0aff).contains(&n) {
            &self.gujarati[n - 0x0a80]
        } else {
            self.other.get(&c).map(Vec::as_slice).unwrap_or(&[])
        }
    }
}
fn unit_index(rows: impl Iterator<Item = UnitRow>) -> UnitIndex {
    let mut out = UnitIndex {
        ascii: std::array::from_fn(|_| Vec::new()),
        gujarati: std::array::from_fn(|_| Vec::new()),
        other: Default::default(),
    };
    for (key, value) in rows {
        if let Some(last) = key.chars().last() {
            let n = last as usize;
            if n < 128 {
                out.ascii[n].push((key, value));
            } else if (0x0a80..=0x0aff).contains(&n) {
                out.gujarati[n - 0x0a80].push((key, value));
            } else {
                out.other.entry(last).or_default().push((key, value));
            }
        }
    }
    out
}
static UNITS: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| unit_index(rows(UNIT).chain(rows(FORMAL))));
static QUARTERS: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| {
    unit_index(rows(QUARTER1).chain(QUARTER2.lines().map(|line| {
        let word = line.trim_end_matches('\r');
        (word, word)
    })))
});
const CITIES: &str = include_str!("../../../data/gu/address/cities.tsv");
const STATES: &str = include_str!("../../../data/gu/address/states.tsv");
fn range_value(s: &str) -> Vec<(String, i64)> {
    if let Some((a, b)) = s.split_once('.') {
        if let Some(frac) = numbers::digitwise(b) {
            return numbers::graph(a)
                .into_iter()
                .map(|(n, c)| (super::lazy::tag(|| format!("{n} દશાંશ {frac}")), c))
                .collect();
        }
        return Vec::new();
    }
    numbers::graph(s)
}
fn sign(s: &str) -> (&str, &'static str) {
    if let Some(n) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (n, "negative: \"true\" ")
    } else if let Some(n) = s.strip_prefix('~') {
        (n.strip_prefix(' ').unwrap_or(n), "negative: \"આશરે\" ")
    } else if let Some(n) = s.strip_prefix('±') {
        (n.strip_prefix(' ').unwrap_or(n), "negative: \"વત્તા કે ઓછા\" ")
    } else {
        (s, "")
    }
}
pub(crate) fn city_prefix_end(s: &str) -> Option<usize> {
    type Names = std::collections::HashMap<char, Vec<&'static str>>;
    static NAMES: std::sync::LazyLock<Names> = std::sync::LazyLock::new(|| {
        let mut names = Names::new();
        for name in [CITIES, STATES]
            .into_iter()
            .flat_map(|data| data.lines())
            .map(|line| line.trim_end_matches('\r').split('\t').next().unwrap_or(""))
        {
            if let Some(first) = name.chars().next() {
                names.entry(first).or_default().push(name);
            }
        }
        names
    });
    let first = s.chars().next()?;
    NAMES
        .get(&first)?
        .iter()
        .filter_map(|name| {
            let tail = s.strip_prefix(*name)?;
            let tail = tail
                .strip_prefix(',')
                .unwrap_or(tail)
                .trim_start_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
            let mut end = s.len() - tail.len();
            let mut chars = tail.chars();
            for _ in 0..6 {
                let c = chars.next()?;
                numbers::digit(c)?;
                end += c.len_utf8();
            }
            Some(end)
        })
        .max()
}
fn address(s: &str, end: usize, out: &mut Vec<Candidate>, city: bool, keyword: bool) {
    let mut reverse = s.chars().rev();
    if city && (0..6).all(|_| reverse.next().is_some_and(|c| numbers::digit(c).is_some())) {
        for data in [CITIES, STATES] {
            for line in data.lines() {
                let name = line.trim_end_matches('\r').split('\t').next().unwrap_or("");
                if let Some(t) = s.strip_prefix(name) {
                    let t = t
                        .strip_prefix(',')
                        .unwrap_or(t)
                        .trim_start_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
                    if t.chars().count() == 6 {
                        if let Some(n) = numbers::digitwise(t) {
                            out.push(Candidate{end,cost:cost(2.1),rank:6,tag:super::lazy::tag(||format!("measure {{ units: \"address\" cardinal {{ integer: \"{name} {n}\" }} preserve_order: true }}"))});
                        }
                    }
                }
            }
        }
    }
    if keyword {
        super::address::classify(s, end, out);
    }
}
fn emit(out: &mut Vec<Candidate>, end: usize, nested: impl FnOnce() -> String, unit: &str, c: i64) {
    out.push(Candidate {
        end,
        cost: c,
        rank: 6,
        tag: super::lazy::tag(|| format!("measure {{ {} units: \"{unit}\" }}", nested())),
    });
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    classify_hinted(s, end, out, true, super::address::has_keyword(s), s.contains(|c| matches!(c, 'x' | 'X' | '*')));
}
pub(crate) fn classify_hinted(
    s: &str,
    end: usize,
    out: &mut Vec<Candidate>,
    city: bool,
    keyword: bool,
    dimension: bool,
) {
    address(s, end, out, city, keyword);
    let Some(first) = s.chars().next() else { return };
    if numbers::digit(first).is_none() && !matches!(first, '-' | '−' | '~' | '±') {
        return;
    }
    let last = s.chars().last().unwrap();
    let units = UNITS.get(last);
    let quarters = QUARTERS.get(last);
    let ends_with_unit = !units.is_empty() || !quarters.is_empty();
    if !ends_with_unit && !dimension {
        return;
    }
    if let Some(n) = s.strip_suffix('s') {
        let chars: Vec<_> = n.chars().collect();
        let decade = (chars.len() == 2 && !matches!(chars[0], '0' | '૦')
            || chars.len() == 4 && matches!(chars[0], '1' | '2' | '૧' | '૨'))
            && chars.last().is_some_and(|c| matches!(*c, '0' | '૦'))
            && chars.iter().all(|c| numbers::digit(*c).is_some());
        if decade {
            return;
        }
    }
    if dimension {
        for (i, ch) in s.char_indices() {
            if !matches!(ch, 'x' | 'X' | '*') {
                continue;
            }
            let (a, sa) = sign(&s[..i]);
            let (b, sb) = sign(&s[i + ch.len_utf8()..]);
            if a.chars().count() > 7 || b.chars().count() > 7 {
                continue;
            }
            for (x, cx) in numbers::graph(a) {
                for (y, cy) in numbers::graph(b) {
                    out.push(Candidate{end,cost:cost(1.2)+cx+cy,rank:6,tag:super::lazy::tag(||format!("measure {{ cardinal {{ {sa}integer: \"{x}\" }} units: \"બાય\" }} }} tokens {{ cardinal {{ {sb}integer: \"{y}\" }}"))});
                }
            }
        }
    }
    for &(unit, word) in units {
        let Some(pre) = s.strip_suffix(unit) else { continue };
        let n = pre.trim_end_matches(|c| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
        if n.is_empty() {
            continue;
        }
        for separator in ['-', '–', '—'] {
            if let Some((a, b)) = n.split_once(separator) {
                let spaced = a.ends_with(' ') && b.starts_with(' ');
                let a = if spaced { a.strip_suffix(' ').unwrap_or(a) } else { a };
                let b = if spaced { b.strip_prefix(' ').unwrap_or(b) } else { b };
                if a.chars().filter(|c| numbers::digit(*c).is_some()).count() <= 7
                    && b.chars().filter(|c| numbers::digit(*c).is_some()).count() <= 7
                {
                    for (x, cx) in range_value(a) {
                        for (y, cy) in range_value(b) {
                            emit(
                                out,
                                end,
                                || format!("cardinal {{ integer: \"{x} થી {y}\" }}"),
                                word,
                                cost(1.05) + cx + cy,
                            );
                        }
                    }
                }
            }
        }
        let mut tmp = Vec::new();
        numeric::classify(n, end, &mut tmp);
        for cand in tmp {
            let is_card = cand.rank == 0;
            let is_dec = cand.rank == 1;
            if !is_card && !is_dec {
                continue;
            }
            if n.contains(',')
                || (is_card && cand.cost > cost(1.1))
                || (is_dec && (!n.contains('.') || !n.chars().last().is_some_and(|c| numbers::digit(c).is_some())))
                || (n.chars().filter(|c| numbers::digit(*c).is_some()).count() > 7 && unit != "yr")
            {
                continue;
            }
            if unit == "yr" && is_card {
                if n.chars().count() <= 3 {
                    emit(out, end, || cand.tag.clone(), "વર્ષ", cand.cost + cost(-0.1));
                }
                if let Some((_, formal)) = rows(FORMAL).find(|(k, _)| *k == unit) {
                    emit(out, end, || cand.tag.clone(), formal, cand.cost + cost(-0.05));
                }
            } else if unit == "yr" && is_dec {
                if let Some((_, formal)) = rows(FORMAL).find(|(k, _)| *k == unit) {
                    emit(out, end, || cand.tag.clone(), formal, cand.cost + cost(-0.05));
                }
            } else if unit != "yr" {
                emit(out, end, || cand.tag.clone(), word, cand.cost + cost(-0.05));
            }
        }
    }
    for &(unit, word) in quarters {
        let Some(pre) = s.strip_suffix(unit) else { continue };
        let n = pre.trim_end_matches(|c| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
        let (n, sign) = if let Some(x) = n.strip_prefix('-').or_else(|| n.strip_prefix('−')) {
            (x, "negative: \"true\" ")
        } else if let Some(x) = n.strip_prefix('~') {
            (x.strip_prefix(' ').unwrap_or(x), "negative: \"આશરે\" ")
        } else if let Some(x) = n.strip_prefix('±') {
            (x.strip_prefix(' ').unwrap_or(x), "negative: \"વત્તા કે ઓછા\" ")
        } else {
            (n, "")
        };
        for (suffix, kind, weight) in [
            (".5", "સાડા", -0.1),
            (".૫", "સાડા", -0.1),
            (".25", "સવા", -0.1),
            (".૨૫", "સવા", -0.1),
            (".75", "પોણા", -0.5),
            (".૭૫", "પોણા", -0.5),
        ] {
            if let Some(base) = n.strip_suffix(suffix) {
                if kind == "પોણા" {
                    if let Some((_, v)) = rows(PAUNE).find(|(k, _)| *k == base) {
                        emit(
                            out,
                            end,
                            || format!("cardinal {{ {sign}integer: \"{kind} {v}\" }}"),
                            word,
                            cost(1.1 + weight),
                        );
                    }
                } else {
                    for (v, c) in numbers::graph(base) {
                        if base.chars().count() <= 7 {
                            emit(
                                out,
                                end,
                                || format!("cardinal {{ {sign}integer: \"{kind} {v}\" }}"),
                                word,
                                cost(1.1 + weight) + c,
                            );
                        }
                    }
                    if kind == "સાડા" && matches!(base, "1" | "૧" | "2" | "૨") {
                        let v = if matches!(base, "1" | "૧") { "દોઢ" } else { "અઢી" };
                        emit(out, end, || format!("cardinal {{ {sign}integer: \"{v}\" }}"), word, cost(0.9));
                    }
                }
            }
        }
    }
}
