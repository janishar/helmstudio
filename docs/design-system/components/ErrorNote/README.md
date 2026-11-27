# ErrorNote

An error message with an optional Retry (kit.tsx `ErrorNote`): `bad` text, 13px, on `s2`, 12px corners, 8px 12px padding, `role="alert"`.

- The text is the server's own `detail` (every API error is `{"detail": string}`), shown as is: a plain sentence saying what went wrong, and what to do when there is something.
- Retry is a 28px `chip` in `t1`.
- While the StatusBanner shows, a note whose text is the outage's own ("The studio isn’t answering.") is held back: one message, not five.

The consumer provides the message and, if it can be retried, `onRetry`.
