"""Verify the exported PDF contains usable Turkish text and the required live evidence."""
import pathlib
import sys

from pypdf import PdfReader

report = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else 'web-react/test-results/assessment-tr.pdf')
reader = PdfReader(report)
text = '\n'.join(page.extract_text() or '' for page in reader.pages)
for expected in ('Değerlendirme raporu', '287,50', '63,50', 'kayıtsız', '18,16',
                 'ET-2026-001', 'SUC-2026-045', 'Market A', 'Market B', 'Market C', 'sentetik', 'Kütle dengesi'):
    assert expected in text, f'Missing PDF evidence or unreadable Unicode text: {expected}'
assert len(reader.pages) >= 3, 'Report should contain the full assessment evidence'
print(f'PDF verified: {len(reader.pages)} pages, Turkish text and all required evidence present')

if '--preview' in sys.argv[2:]:
    import pymupdf
    with pymupdf.open(report) as pdf:
        for index in range(min(2, len(pdf))):
            pdf[index].get_pixmap(matrix=pymupdf.Matrix(1, 1)).save(
                str(report.with_name(f'assessment-preview-{index + 1}.png')))
