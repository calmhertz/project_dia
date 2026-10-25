# REFERENCE is read-only input

This directory holds a prior working ground-station implementation
(`project_dia`, Dhritvan Space Lab / SJCIT). It is reconnaissance material for
Aagasa, not part of Aagasa.

Rules (spec.md section 25, RULES.md section 8):

- Never built, imported, packaged, linted, or tested as part of Aagasa.
- Never edited. Treat it as a frozen artifact.
- Reuse proven behaviour by porting it deliberately into the Aagasa components,
  not by importing from here.

The reuse / adapt / reject inventory is in `docs/architecture-baseline.md`
section 4. Read it before touching G-550, RTL-SDR, SatDump, TLE, or prediction
code.
