use super::graph_utils::{CARDINALS, digit};

pub fn whitelist(s: &str) -> Option<&'static str> {
    static ENTRIES: std::sync::LazyLock<std::collections::HashMap<&'static str, &'static str>> =
        std::sync::LazyLock::new(|| {
            include_str!("../../../data/or/whitelist_entries.tsv")
                .lines()
                .filter(|line| !line.starts_with('#'))
                .filter_map(|line| line.split_once('\t'))
                .collect()
        });
    ENTRIES.get(s).copied()
}
fn roman_value(s: &str) -> Option<usize> {
    const SMALL: [&str; 30] = [
        "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
        "XVIII", "XIX", "XX", "XXI", "XXII", "XXIII", "XXIV", "XXV", "XXVI", "XXVII", "XXVIII", "XXIX", "XXX",
    ];
    SMALL.iter().position(|x| *x == s).map(|n| n + 1).or_else(|| match s {
        "XL" => Some(40),
        "L" => Some(50),
        "LX" => Some(60),
        "LXX" => Some(70),
        "LXXX" => Some(80),
        "XC" => Some(90),
        _ => None,
    })
}
pub fn roman(s: &str) -> Option<String> {
    const PRE: &[&str] = &[
        "ଅଧ୍ୟାୟ",
        "ଭାଗ",
        "ଖଣ୍ଡ",
        "ପର୍ବ",
        "ଶ୍ରେଣୀ",
        "ଧାରା",
        "ଅନୁଚ୍ଛେଦ",
        "ପରିଚ୍ଛେଦ",
        "Chapter",
        "Part",
        "Volume",
        "Section",
        "Act",
        "Scene",
        "ଲୁଇ",
        "Louis",
        "ହେନରୀ",
        "Henry",
        "ଏଡୱାର୍ଡ",
        "Edward",
        "ଜର୍ଜ",
        "George",
        "ଚାର୍ଲ୍ସ",
        "Charles",
        "ଏଲିଜାବେଥ",
        "Elizabeth",
        "ପୋପ୍",
        "Pope",
    ];
    const SUF: &[&str] = &["ଶତାବ୍ଦୀ", "ଶତାବ୍ଦୀର", "Century", "ଭାଗ", "ଅଧ୍ୟାୟ"];
    for sep in [' ', '-'] {
        if let Some((a, b)) = s.split_once(sep) {
            if PRE.contains(&a) {
                if let Some(n) = roman_value(b) {
                    return Some(format!("{a}{sep}{}", CARDINALS[n]));
                }
            }
            if SUF.contains(&b) {
                if let Some(n) = roman_value(a) {
                    return Some(format!("{}{sep}{b}", CARDINALS[n]));
                }
            }
        }
    }
    None
}
pub fn telephone(s: &str) -> Option<String> {
    let (plus, s) = if let Some(a) = s.strip_prefix('+') { (true, a) } else { (false, s) };
    if !plus && !s.starts_with('(') {
        return None;
    }
    if !s.chars().all(|c| digit(c).is_some() || matches!(c, ' ' | '-' | '(' | ')' | '.')) {
        return None;
    }
    let ds: Vec<_> = s.chars().filter_map(digit).collect();
    if plus {
        if !(7..=16).contains(&ds.len()) {
            return None;
        }
    } else {
        let i = s.find(')')?;
        let before = s[1..i].chars().filter_map(digit).count();
        let after = s[i + 1..].chars().filter_map(digit).count();
        if !(2..=5).contains(&before) || !(7..=16).contains(&after) {
            return None;
        }
    }
    let w = ds.iter().map(|d| CARDINALS[*d as usize]).collect::<Vec<_>>().join(" ");
    Some(if plus { format!("ପ୍ଲସ୍ {w}") } else { w })
}
