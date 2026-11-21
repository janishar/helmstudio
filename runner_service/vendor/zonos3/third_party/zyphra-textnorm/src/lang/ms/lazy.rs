use std::cell::Cell;

thread_local! { static RECOGNIZING: Cell<bool> = const { Cell::new(false) }; }

pub(crate) fn recognizing() -> bool {
    RECOGNIZING.with(Cell::get)
}

pub(crate) fn recognize<T>(f: impl FnOnce() -> T) -> T {
    struct Restore(bool);
    impl Drop for Restore {
        fn drop(&mut self) {
            RECOGNIZING.with(|mode| mode.set(self.0));
        }
    }
    RECOGNIZING.with(|mode| {
        let _restore = Restore(mode.replace(true));
        f()
    })
}
