use super::*;

pub(in crate::lang::el) fn all_cardinal(s: &str) -> Option<String> {
    if let Some(x) = number::cardinal(s) {
        return Some(x);
    }
    if s.len() > 1 && s.starts_with('0') {
        if let Some(x) = number::digits(s) {
            return Some(x);
        }
    }
    for (prefix, word) in [("~", "περίπου "), ("±", "συν πλην "), ("-", "μείον "), ("−", "μείον ")]
    {
        if let Some(rest) = s.strip_prefix(prefix) {
            let value = if prefix == "~" || prefix == "±" {
                if let Some(x) = rest.strip_prefix('-').or_else(|| rest.strip_prefix('−')) {
                    spoken!("μείον {}", number::cardinal(x)?)
                } else {
                    number::cardinal(rest)?
                }
            } else {
                number::cardinal(rest)?
            };
            return Some(spoken!("{word}{value}"));
        }
    }
    for sep in [" - ", " – ", " — ", " με ", "-", "–", "—"] {
        if let Some((a, b)) = s.split_once(sep) {
            return Some(spoken!("{} έως {}", number::cardinal(a)?, number::cardinal(b)?));
        }
    }
    for sep in [", ", ",", " "] {
        if s.contains(sep) {
            if !s.split(sep).all(|x| number::cardinal(x).is_some()) {
                return None;
            }
            if !writing_tags() {
                return Some(String::new());
            }
            return s.split(sep).map(number::cardinal).collect::<Option<Vec<_>>>().map(|x| x.join(", "));
        }
    }
    None
}
