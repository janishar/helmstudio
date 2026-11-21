//! Delay final tag serialization until the lattice chooses a candidate.
use std::cell::Cell;
thread_local! { static BUILD: Cell<bool> = const { Cell::new(true) }; }
struct Restore(bool);
impl Drop for Restore {
    fn drop(&mut self) {
        BUILD.with(|x| x.set(self.0));
    }
}
pub fn with_build<T>(build: bool, f: impl FnOnce() -> T) -> T {
    let _restore = Restore(BUILD.with(|x| x.replace(build)));
    f()
}
pub fn building() -> bool {
    BUILD.with(Cell::get)
}
pub fn tag(f: impl FnOnce() -> String) -> String {
    if BUILD.with(Cell::get) { f() } else { String::new() }
}
