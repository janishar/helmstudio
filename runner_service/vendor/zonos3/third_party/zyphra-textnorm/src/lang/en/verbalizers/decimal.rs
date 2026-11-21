//! `en/verbalizers/decimal.py` and `fraction.py`.

use crate::lang::common::Fields;
use crate::lang::en::verbalizers::ordinal::suffix;
use crate::tag::Dict;

/// `DecimalFst.numbers` over the fields from the reader's position (consumes them all).
pub fn numbers(f: &mut Fields) -> Option<String> {
    let neg = f.opt("negative")?;
    if neg.is_some_and(|n| n != "true") {
        return None;
    }
    let mut out = String::new();
    if neg.is_some() {
        out.push_str("minus ");
    }
    let int = f.opt("integer_part")?;
    let frac = f.opt("fractional_part")?;
    let q = f.opt("quantity")?;
    if frac.is_none() && int.is_none() {
        return None;
    }
    if frac.is_some_and(str::is_empty) || q.is_some_and(str::is_empty) {
        return None;
    }
    if let Some(i) = int {
        out.push_str(i);
    }
    if let Some(fr) = frac {
        if int.is_some() {
            out.push(' ');
        }
        out.push_str("point ");
        out.push_str(fr);
    }
    if let Some(q) = q {
        out.push(' ');
        out.push_str(q);
    }
    Some(out)
}

pub fn decimal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = numbers(&mut f)?;
    f.done().then_some(s)
}

/// `FractionFst.graph` from the reader's position.
pub fn fraction_graph(f: &mut Fields) -> Option<String> {
    let int = f.opt("integer_part")?;
    let num = f.take("numerator")?;
    let den = f.take("denominator")?;
    let dens = match den {
        "one" => "over one".to_string(),
        "two" => "half".to_string(),
        "four" => "quarter".to_string(),
        d => suffix(d),
    };
    let mut g = if num == "one" {
        format!("one {dens}")
    } else {
        let mut g = format!("{num} {dens}");
        if let Some(p) = g.strip_suffix("half") {
            g = format!("{p}halves");
        } else {
            g.push('s');
        }
        g
    };
    if let Some(i) = int {
        g = format!("{i} and {g}");
    }
    if let Some(p) = g.strip_suffix("and one half") {
        g = format!("{p}and a half");
    } else if let Some(p) = g.strip_suffix("over ones") {
        g = format!("{p}over one");
    }
    Some(g)
}

pub fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = fraction_graph(&mut f)?;
    f.done().then_some(s)
}
