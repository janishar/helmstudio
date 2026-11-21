//! `pt/verbalizers/time.py`: the −0.01 feminine hour phrases win over `<hours> horas`.

use crate::lang::common::Fields;
use crate::tag::Dict;

fn hour_phrase(h: &str, out: &mut String) {
    match h {
        "um" => out.push_str("uma hora"),
        "dois" => out.push_str("duas horas"),
        "vinte e um" => out.push_str("vinte e uma horas"),
        "vinte e dois" => out.push_str("vinte e duas horas"),
        _ => {
            out.push_str(h);
            out.push_str(" horas");
        }
    }
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out = String::with_capacity(96);
    hour_phrase(f.take("hours")?, &mut out);
    if let Some(m) = f.opt("minutes")? {
        out.push_str(" e ");
        out.push_str(m);
        if let Some(s) = f.opt("seconds")? {
            out.push_str(" minutos e ");
            out.push_str(s);
            out.push_str(" segundos");
        }
    }
    if let Some(s) = f.opt("suffix")? {
        out.push(' ');
        out.push_str(s);
    }
    f.preserve_order();
    f.done().then_some(out)
}
