use super::*;

struct Unit {
    key: &'static str,
    singular: &'static str,
    plural: &'static str,
    feminine: bool,
    masculine: bool,
}

static UNITS_BY_END: std::sync::LazyLock<std::collections::HashMap<char, Vec<Unit>>> = std::sync::LazyLock::new(|| {
    let mut index: std::collections::HashMap<char, Vec<Unit>> = std::collections::HashMap::new();
    for &(keys, singular, plural) in tables::UNITS {
        let feminine = keys
            .iter()
            .any(|key| ["cal", "kcal", "in", "ίντσες", "lb", "λίβρες", "h", "ώ", "ωρ.", "kWh", "mAh"].contains(key));
        let masculine = keys.iter().any(|key| ["t", "τόνοι", "°", "°C", "℃", "°F", "℉"].contains(key));
        for &key in keys {
            if let Some(last) = key.chars().last() {
                index.entry(last).or_default().push(Unit { key, singular, plural, feminine, masculine });
            }
        }
    }
    index
});

pub(in crate::lang::el) fn plain_number(s: &str) -> Option<String> {
    number::cardinal(s)
        .or_else(|| decimal_core(s))
        .or_else(|| tables::VULGAR.iter().find(|(k, _)| *k == s).map(|(_, v)| spoken!("{v}")))
}
pub(in crate::lang::el) fn split_range(s: &str) -> Option<(&str, &str)> {
    for sep in [" - ", " – ", " — ", "-", "–", "—"] {
        if let Some((a, b)) = s.split_once(sep) {
            if !a.is_empty() && !b.is_empty() {
                return Some((a, b));
            }
        }
    }
    None
}
pub(in crate::lang::el) fn measure(s: &str) -> Option<String> {
    for (p, w) in [("~", "περίπου "), ("±", "συν πλην ")] {
        if let Some(x) = s.strip_prefix(p) {
            if let Some(x) = x.strip_prefix('-').or_else(|| x.strip_prefix('−')) {
                return Some(spoken!("{w}μείον {}", measure_base(x, false)?));
            }
            return Some(spoken!("{w}{}", measure_base(x, false)?));
        }
    }
    if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        return Some(spoken!("μείον {}", measure_base(x, false)?));
    }
    measure_base(s, true)
}
pub(in crate::lang::el) fn measure_base(s: &str, allow_range: bool) -> Option<String> {
    for (suffix, unit) in [("%", "τοις εκατό"), ("‰", "τοις χιλίοις")] {
        if let Some(x) = s.strip_suffix(suffix) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            if allow_range {
                if let Some((a, b)) = split_range(x) {
                    if let (Some(a), Some(b)) = (number::cardinal(a), number::cardinal(b)) {
                        return Some(spoken!("{a} έως {b} {unit}"));
                    }
                }
            }
            return Some(spoken!("{} {unit}", plain_number(x)?));
        }
    }
    let candidates = UNITS_BY_END.get(&s.chars().last()?)?;
    for unit in candidates {
        if let Some(x) = s.strip_suffix(unit.key) {
            let x = x.strip_suffix(' ').unwrap_or(x);
            let (sing, plural, feminine, masculine) = (unit.singular, unit.plural, unit.feminine, unit.masculine);
            let inflect = |v: String| if feminine || masculine { number::agree(&v, masculine) } else { v };
            if allow_range {
                if let Some((a, b)) = split_range(x) {
                    if let (Some(a), Some(b)) = (number::cardinal(a), number::cardinal(b)) {
                        return Some(spoken!("{} έως {} {plural}", inflect(a), inflect(b)));
                    }
                }
            }
            if x == "1" {
                return Some(spoken!(
                    "{} {sing}",
                    if feminine {
                        "μία"
                    } else if masculine {
                        "ένας"
                    } else {
                        "ένα"
                    }
                ));
            }
            if let Some(v) = number::cardinal(x) {
                return Some(spoken!("{} {plural}", inflect(v)));
            }
            if let Some(v) = decimal_core(x)
                .or_else(|| tables::VULGAR.iter().find(|(key, _)| *key == x).map(|(_, v)| spoken!("{v}")))
            {
                return Some(spoken!("{v} {plural}"));
            }
        }
    }
    None
}
