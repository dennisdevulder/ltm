//! Name reservation for the ltm project.
//!
//! The real CLI is a Go binary. See <https://ltm-cli.dev>.

/// Returns a placeholder string explaining the reservation.
pub fn placeholder() -> &'static str {
    "ltm-cli on crates.io is a name reservation. \
     Install the real binary from https://ltm-cli.dev"
}
