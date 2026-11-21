//! `Normalizer._permute`, `generate_permutations`, `_estimate_number_of_permutations_in_nested_dict` and
//! `_split_tokens_to_reduce_number_of_permutations` (`normalize.py`).

use crate::tag::{Dict, Value};

pub const MAX_PERMUTATIONS_PER_SPLIT: u128 = 729;

/// `_estimate_number_of_permutations_in_nested_dict`.
pub fn estimate(d: &Dict) -> u128 {
    let mut n: u128 = 1;
    for (_, v) in d {
        if let Value::Dict(inner) = v {
            n = n.saturating_mul(estimate(inner));
        }
    }
    n.saturating_mul(factorial(d.len()))
}

fn factorial(n: usize) -> u128 {
    (1..=n as u128).fold(1u128, |a, b| a.saturating_mul(b))
}

/// The unsplittable-token `ValueError` raised outside the per-split `try`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct TooManyPermutations;

/// `_split_tokens_to_reduce_number_of_permutations` (max 729).
pub fn split<'t, 'a>(tokens: &'t [Dict<'a>]) -> Result<Vec<&'t [Dict<'a>]>, TooManyPermutations> {
    let mut splits = Vec::new();
    let mut prev = 0usize;
    let mut cur: u128 = 1;
    for (i, tok) in tokens.iter().enumerate() {
        let n = estimate(tok);
        if n.saturating_mul(cur) > MAX_PERMUTATIONS_PER_SPLIT {
            splits.push(&tokens[prev..i]);
            prev = i;
            cur = 1;
        }
        if n > MAX_PERMUTATIONS_PER_SPLIT {
            return Err(TooManyPermutations);
        }
        cur = cur.saturating_mul(n);
    }
    splits.push(&tokens[prev..]);
    Ok(splits)
}

/// Whether `_permute` raises for this dict (a `None` value anywhere in it).
pub fn has_none(d: &Dict) -> bool {
    d.iter().any(|(_, v)| match v {
        Value::None => true,
        Value::Dict(inner) => has_none(inner),
        _ => false,
    })
}

/// `itertools.permutations(range(n))` order: lexicographic over index sequences.
fn next_permutation(p: &mut [usize]) -> bool {
    if p.len() < 2 {
        return false;
    }
    let mut i = p.len() - 1;
    while i > 0 && p[i - 1] >= p[i] {
        i -= 1;
    }
    if i == 0 {
        return false;
    }
    let mut j = p.len() - 1;
    while p[j] <= p[i - 1] {
        j -= 1;
    }
    p.swap(i - 1, j);
    p[i..].reverse();
    true
}

/// Every field ordering `_permute(d)` serializes, in its order, as reordered dict trees. Visits until `f` returns
/// `Some`. The caller has checked [`has_none`] (the serialization raises before yielding anything otherwise).
pub fn first_ordering<'a, T>(d: &Dict<'a>, f: &mut dyn FnMut(&Dict<'a>) -> Option<T>) -> Option<T> {
    // The first ordering is `d` itself (identity at every level): visit it without copying. The enumeration below
    // starts with that same ordering (its first visit), so it is skipped there.
    if let Some(r) = f(d) {
        return Some(r);
    }
    let mut first = true;
    let mut rest = |ordering: &Dict<'a>| {
        if std::mem::take(&mut first) {
            return None;
        }
        f(ordering)
    };
    let preserve = d.iter().any(|(k, _)| k == "preserve_order");
    let mut order: Vec<usize> = (0..d.len()).collect();
    let mut current: Dict<'a> = Vec::with_capacity(d.len());
    loop {
        if let Some(r) = product(d, &order, 0, &mut current, &mut rest) {
            return Some(r);
        }
        if preserve || !next_permutation(&mut order) {
            return None;
        }
    }
}

/// Cartesian product over the nested dicts' orderings, earlier fields slowest (`itertools.product(subl, ...)`).
fn product<'a, T>(
    d: &Dict<'a>,
    order: &[usize],
    k: usize,
    current: &mut Dict<'a>,
    f: &mut dyn FnMut(&Dict<'a>) -> Option<T>,
) -> Option<T> {
    if k == order.len() {
        return f(current);
    }
    let (key, value) = &d[order[k]];
    match value {
        Value::Dict(inner) => first_ordering(inner, &mut |inner_ordering: &Dict<'a>| {
            current.push((key.clone(), Value::Dict(inner_ordering.clone())));
            let r = product(d, order, k + 1, current, f);
            current.pop();
            r
        }),
        other => {
            current.push((key.clone(), other.clone()));
            let r = product(d, order, k + 1, current, f);
            current.pop();
            r
        }
    }
}

/// `generate_permutations(split)`: every ordering of the whole split, in its order (cartesian product over the
/// tokens, the first token slowest, each token in [`first_ordering`] order). Visits until `f` returns `Some`. The
/// caller has checked [`has_none`] on every token.
pub fn split_orderings<'a, T>(split: &[Dict<'a>], f: &mut dyn FnMut(&[Dict<'a>]) -> Option<T>) -> Option<T> {
    fn rec<'a, T>(
        split: &[Dict<'a>],
        acc: &mut Vec<Dict<'a>>,
        f: &mut dyn FnMut(&[Dict<'a>]) -> Option<T>,
    ) -> Option<T> {
        let Some((first, rest)) = split.split_first() else { return f(acc) };
        first_ordering(first, &mut |ordering: &Dict<'a>| {
            acc.push(ordering.clone());
            let r = rec(rest, acc, f);
            acc.pop();
            r
        })
    }
    rec(split, &mut Vec::with_capacity(split.len()), f)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(v: &str) -> Value<'_> {
        Value::Str(v.into())
    }

    #[test]
    fn itertools_order() {
        let d: Dict = vec![("a".into(), s("1")), ("b".into(), s("2")), ("c".into(), s("3"))];
        let mut seen = Vec::new();
        first_ordering::<()>(&d, &mut |o| {
            seen.push(o.iter().map(|(k, _)| k.as_ref()).collect::<String>());
            None
        });
        assert_eq!(seen, ["abc", "acb", "bac", "bca", "cab", "cba"]);
    }

    #[test]
    fn nested_product_and_preserve_order() {
        let inner: Dict = vec![("x".into(), s("1")), ("y".into(), s("2"))];
        let d: Dict = vec![("p".into(), Value::Dict(inner.clone())), ("q".into(), s("3"))];
        let mut seen = Vec::new();
        first_ordering::<()>(&d, &mut |o| {
            let mut out = String::new();
            for (k, v) in o {
                out.push_str(k);
                if let Value::Dict(i) = v {
                    for (ik, _) in i {
                        out.push_str(ik);
                    }
                }
            }
            seen.push(out);
            None
        });
        assert_eq!(seen, ["pxyq", "pyxq", "qpxy", "qpyx"]);
        let d: Dict = vec![("b".into(), s("1")), ("a".into(), s("2")), ("preserve_order".into(), Value::True)];
        let mut n = 0;
        first_ordering::<()>(&d, &mut |_| {
            n += 1;
            None
        });
        assert_eq!(n, 1);
    }

    #[test]
    fn estimate_and_split() {
        let date: Dict = vec![(
            "tokens".into(),
            Value::Dict(vec![(
                "date".into(),
                Value::Dict(vec![("month".into(), s("m")), ("day".into(), s("d")), ("year".into(), s("y"))]),
            )]),
        )];
        assert_eq!(estimate(&date), 6);
        let toks = vec![date.clone(); 5];
        let splits = split(&toks).unwrap();
        assert_eq!(splits.iter().map(|s| s.len()).collect::<Vec<_>>(), [3, 2]); // 6*6*6=216, *6=1296 > 729
    }

    #[test]
    fn split_orderings_match_generate_permutations() {
        // NeMo: generate_permutations([{"tokens": {"a": "1", "b": "2"}}, {"tokens": {"c": "3"}}]) yields, in order,
        // ' tokens { a: "1" b: "2"  }  tokens { c: "3"  } ' then ' tokens { b: "2" a: "1"  }  tokens { c: "3"  } '.
        let tok = |body: Dict<'static>| -> Dict<'static> { vec![("tokens".into(), Value::Dict(body))] };
        let split = [tok(vec![("a".into(), s("1")), ("b".into(), s("2"))]), tok(vec![("c".into(), s("3"))])];
        let mut seen = Vec::new();
        split_orderings::<()>(&split, &mut |ordering| {
            let mut out = String::new();
            for t in ordering {
                crate::tag::serialize(t, &mut out);
            }
            seen.push(out);
            None
        });
        assert_eq!(
            seen,
            [
                " tokens { a: \"1\" b: \"2\"  }  tokens { c: \"3\"  } ",
                " tokens { b: \"2\" a: \"1\"  }  tokens { c: \"3\"  } "
            ]
        );
    }
}
