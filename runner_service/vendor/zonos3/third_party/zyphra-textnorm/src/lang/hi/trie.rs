//! Immutable byte trie for source lexicons.

#[derive(Default)]
struct Node {
    children: Vec<(u8, usize)>,
    terminal: bool,
}

#[derive(Default)]
pub(super) struct Trie {
    nodes: Vec<Node>,
    starts: [u64; 4],
    other_starts: Vec<char>,
}

impl Trie {
    pub(super) fn from_keys(keys: impl IntoIterator<Item = &'static str>) -> Self {
        let mut trie = Self { nodes: vec![Node::default()], ..Self::default() };
        for key in keys {
            if let Some(first) = key.chars().next() {
                let c = first as u32;
                let bit = if c < 128 {
                    Some(c as usize)
                } else if (0x900..0x980).contains(&c) {
                    Some(128 + (c - 0x900) as usize)
                } else {
                    None
                };
                if let Some(bit) = bit {
                    trie.starts[bit / 64] |= 1u64 << (bit % 64);
                } else {
                    trie.other_starts.push(first);
                }
            }
            let mut node = 0;
            for &byte in key.as_bytes() {
                node = if let Some(&(_, next)) = trie.nodes[node].children.iter().find(|&&(b, _)| b == byte) {
                    next
                } else {
                    let next = trie.nodes.len();
                    trie.nodes.push(Node::default());
                    trie.nodes[node].children.push((byte, next));
                    next
                };
            }
            trie.nodes[node].terminal = true;
        }
        trie.other_starts.sort_unstable();
        trie.other_starts.dedup();
        for node in &mut trie.nodes {
            node.children.sort_unstable_by_key(|&(byte, _)| byte);
        }
        trie
    }
    fn may_start(&self, first: char) -> bool {
        let c = first as u32;
        let bit = if c < 128 {
            Some(c as usize)
        } else if (0x900..0x980).contains(&c) {
            Some(128 + (c - 0x900) as usize)
        } else {
            None
        };
        match bit {
            Some(bit) => self.starts[bit / 64] & (1u64 << (bit % 64)) != 0,
            None => self.other_starts.binary_search(&first).is_ok(),
        }
    }
    pub(super) fn has_prefix(&self, text: &str) -> bool {
        let mut node = 0;
        for byte in text.bytes() {
            let children = &self.nodes[node].children;
            let Ok(index) = children.binary_search_by_key(&byte, |&(b, _)| b) else { return false };
            node = children[index].1;
        }
        true
    }
    pub(super) fn last_start(&self, text: &str) -> Option<usize> {
        for (index, first) in text.char_indices().rev() {
            if !self.may_start(first) {
                continue;
            }
            let mut found = false;
            self.matching_prefixes(&text[index..], |_| found = true);
            if found {
                return Some(text[..index].chars().count());
            }
        }
        None
    }

    pub(super) fn matching_prefixes(&self, text: &str, mut emit: impl FnMut(usize)) {
        let mut node = 0;
        for (i, byte) in text.bytes().enumerate() {
            let children = &self.nodes[node].children;
            let Ok(index) = children.binary_search_by_key(&byte, |&(b, _)| b) else { break };
            node = children[index].1;
            if self.nodes[node].terminal {
                emit(i + 1);
            }
        }
    }

    pub(super) fn contains_in(&self, text: &str) -> bool {
        text.char_indices().any(|(i, first)| {
            if !self.may_start(first) {
                return false;
            }
            let mut found = false;
            self.matching_prefixes(&text[i..], |_| found = true);
            found
        })
    }
}
