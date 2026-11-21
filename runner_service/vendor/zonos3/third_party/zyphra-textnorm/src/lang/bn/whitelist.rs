//! Bengali Whitelist classifier.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const ABBREVIATIONS_DATA: &str = include_str!("../../../data/bn/whitelist/abbreviations.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static ABBREVIATIONS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ABBREVIATIONS_DATA).collect());

static ABBREVIATIONS_BY_FIRST: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = std::collections::HashMap::<char, Vec<&'static str>>::new();
    for &key in ABBREVIATIONS.keys() {
        if let Some(first) = key.chars().next() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});

fn tag_name(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 16);
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}

pub fn ends_with_abbreviation(input: &str) -> Vec<usize> {
    let mut ends = Vec::new();
    for &k in input.chars().next().and_then(|c| ABBREVIATIONS_BY_FIRST.get(&c)).into_iter().flatten() {
        if input.starts_with(k) {
            ends.push(k.len());
        }
    }
    ends
}

pub fn is_dynamic_start(input: &str, first: char) -> bool {
    first.is_ascii_uppercase() || matches!(first, '§' | '©' | '№' | '#' | 'N') || input.starts_with("নং")
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.01);

    // 1. Static abbreviations table
    if let Some(&spoken) = ABBREVIATIONS.get(s) {
        out.push(Candidate { end, cost: top_weight, rank: 0, tag: super::lazy::tag(move || tag_name(spoken)) });
    }

    // 2. Acronyms: e.g. U.S.A., U. S. A., A.B.C.
    // At least 3 uppercase letters separated by "." or ". "
    if s.chars().next().is_some_and(|c| c.is_ascii_uppercase()) {
        for sep in [". ", "."] {
            let parts: Vec<&str> = s.split(sep).collect();
            // Optional trailing dot might make last part empty
            let core_parts = if parts.last() == Some(&"") { &parts[..parts.len() - 1] } else { &parts[..] };
            if core_parts.len() >= 3
                && core_parts.iter().all(|p| p.len() == 1 && p.chars().next().unwrap().is_ascii_uppercase())
            {
                let spoken = core_parts.join(" ");
                out.push(Candidate {
                    end,
                    cost: top_weight,
                    rank: 0,
                    tag: super::lazy::tag(move || tag_name(&spoken)),
                });
                break;
            }
        }
    }

    // 3. Keyed cardinal:
    // § -> ধারা <cardinal>
    if let Some(rest) = s.strip_prefix('§') {
        let rest = rest.trim_start_matches(' ');
        for (w, c) in numbers::graph(rest) {
            let full = format!("ধারা {w}");
            out.push(Candidate { end, cost: top_weight + c, rank: 0, tag: super::lazy::tag(move || tag_name(&full)) });
        }
    }

    // © -> কপিরাইট <cardinal>
    if let Some(rest) = s.strip_prefix('©') {
        let rest = rest.trim_start_matches(' ');
        for (w, c) in numbers::graph(rest) {
            let full = format!("কপিরাইট {w}");
            out.push(Candidate { end, cost: top_weight + c, rank: 0, tag: super::lazy::tag(move || tag_name(&full)) });
        }
    }

    // নং, নং., No., Nº, №, # -> নম্বর <cardinal>
    for prefix in ["নং.", "নং", "No.", "Nº", "№", "#"] {
        if let Some(rest) = s.strip_prefix(prefix) {
            let rest = rest.trim_start_matches(' ');
            if !rest.is_empty() {
                for (w, c) in numbers::graph(rest) {
                    let full = format!("নম্বর {w}");
                    out.push(Candidate {
                        end,
                        cost: top_weight + c,
                        rank: 0,
                        tag: super::lazy::tag(move || tag_name(&full)),
                    });
                }
            }
        }
    }
}
