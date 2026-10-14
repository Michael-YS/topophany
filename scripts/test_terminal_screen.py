import unittest

from terminal_screen import TerminalScreen


class TerminalScreenTests(unittest.TestCase):
    def test_failover_with_only_changed_digit(self):
        screen = TerminalScreen(40, 2)
        screen.feed(b'\x1b[1;1Hcollector-0')
        screen.feed(b'\x1b[?2026h\x1b[1;11H1\x1b[?2026l')
        self.assertIn('collector-1', screen.text())
        self.assertNotIn('collector-0', screen.text())

    def test_fragmented_utf8_cursor_style_and_osc(self):
        screen = TerminalScreen(40, 2)
        stream = '\x1b[2J\x1b[2;1H\x1b[31m界e\u0301\x1b[0m\x1b]2;hidden title\x07\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\'
        for byte in stream.encode():
            screen.feed(bytes([byte]))
        self.assertTrue(screen.text().splitlines()[1].startswith('界e\u0301link'))
        self.assertNotIn('hidden', screen.text())
        self.assertNotIn('https', screen.text())
        screen.feed(b'\x1b[2;4HX')
        self.assertTrue(screen.text().splitlines()[1].startswith('界e\u0301Xink'))

    def test_clear_removes_previous_screen(self):
        screen = TerminalScreen(20, 2)
        screen.feed(b'collector-0\x1b[2;1Hold state')
        screen.feed(b'\x1b[Hnew\x1b[J')
        self.assertEqual(screen.text(), 'new' + ' ' * 17 + '\n' + ' ' * 20)
        screen.feed(b'\x1b[2J')
        self.assertEqual(screen.text().strip(), '')


if __name__ == '__main__':
    unittest.main()
