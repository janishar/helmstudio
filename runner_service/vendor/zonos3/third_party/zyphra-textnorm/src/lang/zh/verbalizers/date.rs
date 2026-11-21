//! `zh/verbalizers/date.py`: `graph_date` (`year* month* day*`, each with `年`/`月`/`日` appended), `graph_date_era`
//! (`era year [month [day]]`) and `graph_range` (`[range] graph_date range graph_date`, either date possibly empty).
//! A range value is passed through, except `- ~ —— —` which become `到` (the oracle's pick among the equal-cost
//! `NOT_QUOTE*` and `cross(symbol, "到")` outputs, checked with `--tagged`).

use super::Cur;

#[derive(Clone, Copy, PartialEq)]
enum K {
    Era,
    Year,
    Month,
    Day,
    Range,
}

const KEYS: [(&str, K); 5] =
    [("era: \"", K::Era), ("year: \"", K::Year), ("month: \"", K::Month), ("day: \"", K::Day), ("range: \"", K::Range)];

/// `graph_date` on `kinds[i..]`: the end of the longest `year? month? day?` run (keys appear at most once per token).
fn date_run(kinds: &[K], mut i: usize) -> usize {
    for k in [K::Year, K::Month, K::Day] {
        if kinds.get(i) == Some(&k) {
            i += 1;
        }
    }
    i
}

/// `graph_date | graph_date_era | graph_range` accepts the field sequence `kinds`.
fn accepts(kinds: &[K]) -> bool {
    let n = kinds.len();
    let era = matches!(kinds, [K::Era, K::Year] | [K::Era, K::Year, K::Month] | [K::Era, K::Year, K::Month, K::Day]);
    // graph_range: [range] graph_date range graph_date (either date may be empty)
    let range = |i: usize| {
        let j = date_run(kinds, i);
        kinds.get(j) == Some(&K::Range) && date_run(kinds, j + 1) == n
    };
    date_run(kinds, 0) == n || era || range(0) || (kinds.first() == Some(&K::Range) && range(1))
}

/// Appends one field's output.
fn emit_one(k: K, v: &str, out: &mut String) {
    match k {
        K::Range if matches!(v, "-" | "~" | "——" | "—") => out.push('到'),
        _ => out.push_str(v),
    }
    match k {
        K::Year => out.push('年'),
        K::Month => out.push('月'),
        K::Day => out.push('日'),
        _ => {}
    }
}

/// [`date`] on parsed fields whose values hold no `"` (the serialized text then reads back as exactly these fields):
/// `Some(accepted)`, or `None` for a key [`date`] does not read.
pub fn direct(fields: &[(&str, &str)], out: &mut String) -> Option<bool> {
    let mut kinds = [K::Era; 8];
    if fields.len() > kinds.len() {
        return None;
    }
    for (slot, (key, _)) in kinds.iter_mut().zip(fields) {
        *slot = match *key {
            "era" => K::Era,
            "year" => K::Year,
            "month" => K::Month,
            "day" => K::Day,
            "range" => K::Range,
            _ => return None,
        };
    }
    let kinds = &kinds[..fields.len()];
    if !accepts(kinds) {
        return Some(false);
    }
    for (&k, (_, v)) in kinds.iter().zip(fields) {
        emit_one(k, v, out);
    }
    Some(true)
}

fn emit(f: &[(K, &[char])], out: &mut String) {
    for &(k, v) in f {
        if k == K::Range && matches!(v, ['-'] | ['~'] | ['—', '—'] | ['—']) {
            out.push('到');
            continue;
        }
        out.extend(v);
        match k {
            K::Year => out.push('年'),
            K::Month => out.push('月'),
            K::Day => out.push('日'),
            _ => {}
        }
    }
}

pub fn date(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    // every accepted field sequence has at most 8 fields, so a longer one is rejected as soon as it overflows
    let mut buf: [(K, &[char]); 8] = [(K::Era, &[]); 8];
    let mut n = 0;
    loop {
        let before = *c;
        c.ws();
        let Some(&(open, k)) = KEYS.iter().find(|(open, _)| c.peek(open)) else {
            *c = before;
            break;
        };
        let Some(v) = c.field(open) else {
            *c = save;
            return false;
        };
        if n == buf.len() {
            *c = save;
            return false;
        }
        buf[n] = (k, v);
        n += 1;
    }
    // Parsed tokens hold each key once (the parser overwrites duplicates), so `closure(component)` is `component?`.
    let f = &buf[..n];
    let mut kinds = [K::Era; 8];
    for (slot, x) in kinds.iter_mut().zip(f) {
        *slot = x.0;
    }
    if !accepts(&kinds[..n]) {
        *c = save;
        return false;
    }
    emit(f, out);
    true
}
