"""Read the cursor-addressed output emitted by Topophany's TUI.

This is a screen model for the PTY acceptance test, not a general VT emulator.
It preserves partial UTF-8 and escape sequences across reads.
"""
import codecs
import re
import unicodedata


class TerminalScreen:
    def __init__(self, width, height):
        self.width, self.height = width, height
        self.cells = [[' '] * width for _ in range(height)]
        self.row = self.col = 0
        self.pending = ''
        self.decoder = codecs.getincrementaldecoder('utf-8')(errors='replace')

    def feed(self, data):
        self.pending += self.decoder.decode(data)
        while self.pending:
            if self.pending.startswith('\x1b'):
                if len(self.pending) < 2:
                    return
                if self.pending[1] == '[':
                    match = re.match(r'\x1b\[([0-?]*)([ -/]*)([@-~])', self.pending)
                    if not match:
                        return
                    params, _, command = match.groups()
                    self.pending = self.pending[match.end():]
                    self._csi(params, command)
                elif self.pending[1] == ']':
                    match = re.search(r'\x07|\x1b\\', self.pending)
                    if not match:
                        return
                    self.pending = self.pending[match.end():]
                else:
                    raise ValueError('unsupported terminal escape')
                continue
            char, self.pending = self.pending[0], self.pending[1:]
            if char == '\r':
                self.col = 0
            elif char == '\n':
                self.row = min(self.row + 1, self.height - 1)
            elif char >= ' ':
                width = 0 if unicodedata.combining(char) else (2 if unicodedata.east_asian_width(char) in 'WF' else 1)
                if width == 0:
                    if 0 < self.col <= self.width:
                        self.cells[self.row][self.col - 1] += char
                    continue
                if self.col < self.width:
                    self.cells[self.row][self.col] = char
                    if width == 2 and self.col + 1 < self.width:
                        self.cells[self.row][self.col + 1] = ''
                self.col += width

    def _csi(self, params, command):
        if command in ('H', 'f'):
            coords = params.split(';')
            self.row = min(max(int(coords[0] or 1), 1), self.height) - 1
            self.col = min(max(int(coords[1] or 1) if len(coords) > 1 else 1, 1), self.width) - 1
        elif command == 'J':
            mode = int(params or 0)
            for row in range(self.height):
                for col in range(self.width):
                    if mode == 2 or (mode == 0 and (row, col) >= (self.row, self.col)):
                        self.cells[row][col] = ' '
        elif command not in ('m', 'h', 'l', 't'):
            raise ValueError('unsupported terminal CSI command: ' + command)

    def text(self):
        return '\n'.join(''.join(row) for row in self.cells)
