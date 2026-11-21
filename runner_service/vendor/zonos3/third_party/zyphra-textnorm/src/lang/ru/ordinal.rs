// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Ordinal classifier, ported from ru/taggers/ordinal.py.

use super::number::{OrdinalClass, OrdinalForm, ordinal, ordinal_ending, ordinal_stem};
use crate::lattice::Candidate;
use crate::text::cost;

const SUFFIXES: &[(&str, OrdinalForm)] = &[
    ("й", OrdinalForm::MascNom),
    ("ый", OrdinalForm::MascNom),
    ("ой", OrdinalForm::MascNom),
    ("ий", OrdinalForm::MascNom),
    ("я", OrdinalForm::FemNom),
    ("ая", OrdinalForm::FemNom),
    ("ья", OrdinalForm::FemNom),
    ("ые", OrdinalForm::PlNom),
    ("ое", OrdinalForm::NeutNom),
    ("ье", OrdinalForm::NeutNom),
    ("и", OrdinalForm::PlNom),
    ("ьи", OrdinalForm::PlNom),
    ("го", OrdinalForm::MascGen),
    ("ого", OrdinalForm::MascGen),
    ("его", OrdinalForm::MascGen),
    ("ей", OrdinalForm::FemGen),
    ("му", OrdinalForm::MascDat),
    ("ому", OrdinalForm::MascDat),
    ("ему", OrdinalForm::MascDat),
    ("м", OrdinalForm::MascPrep),
    ("ом", OrdinalForm::MascPrep),
    ("ем", OrdinalForm::MascPrep),
    ("ым", OrdinalForm::MascIns),
    ("им", OrdinalForm::MascIns),
    ("х", OrdinalForm::PlGen),
    ("ых", OrdinalForm::PlGen),
    ("их", OrdinalForm::PlGen),
    ("ми", OrdinalForm::PlIns),
    ("ыми", OrdinalForm::PlIns),
    ("ю", OrdinalForm::FemAcc),
    ("ую", OrdinalForm::FemAcc),
];

pub fn render_ordinal_suffix(s: &str) -> Option<String> {
    let (num_str, suff) = s.split_once('-')?;
    if num_str.is_empty() || !num_str.chars().all(|c| c.is_ascii_digit()) {
        return None;
    }
    if num_str.len() > 12 && num_str.bytes().any(|b| b != b'0') {
        return None;
    }
    let n: u64 = if num_str.bytes().all(|b| b == b'0') { 0 } else { num_str.parse().ok()? };

    if suff == "х" {
        if n % 10 != 0 {
            return None;
        }
        let (stem, cls) = ordinal_stem(n);
        let end = ordinal_ending(cls, OrdinalForm::PlGen);
        return Some(format!("{stem}{end}"));
    }

    if suff == "е" {
        let (stem, cls) = ordinal_stem(n);
        let form = match cls {
            OrdinalClass::Soft => OrdinalForm::NeutNom,
            _ => OrdinalForm::PlNom,
        };
        let end = ordinal_ending(cls, form);
        return Some(format!("{stem}{end}"));
    }

    for &(written_suff, form) in SUFFIXES {
        if suff == written_suff {
            let (stem, cls) = ordinal_stem(n);
            let end = ordinal_ending(cls, form);
            return Some(format!("{stem}{end}"));
        }
    }

    None
}

pub fn render_intl_decade(s: &str) -> Option<String> {
    let s_clean = s.strip_suffix("'s").or_else(|| s.strip_suffix('s'))?;
    if s_clean.len() == 4
        && (s_clean.starts_with("18") || s_clean.starts_with("19") || s_clean.starts_with("20"))
        && s_clean.ends_with('0')
        && s_clean.chars().all(|c| c.is_ascii_digit())
    {
        let n: u64 = s_clean.parse().ok()?;
        return Some(ordinal(n, OrdinalForm::PlNom));
    }
    if s_clean.starts_with('\'') && s_clean.len() == 3 && s_clean.ends_with('0') {
        let digits = &s_clean[1..];
        if digits.chars().all(|c| c.is_ascii_digit()) && !digits.starts_with('0') {
            let n: u64 = digits.parse().ok()?;
            return Some(ordinal(n, OrdinalForm::PlNom));
        }
    }
    None
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if render_intl_decade(s).is_some() {
        out.push(Candidate { end, cost: cost(1.1 - 0.1), rank: 1, tag: String::new() });
    } else if render_ordinal_suffix(s).is_some() {
        out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(spoken) = render_intl_decade(s).or_else(|| render_ordinal_suffix(s)) {
        candidate.tag = format!("ordinal {{ integer: \"{spoken}\" }}");
    }
}
