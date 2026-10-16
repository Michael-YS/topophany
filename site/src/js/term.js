// A terminal screen on a canvas. Cells are drawn the way a good terminal
// emulator draws them: text from the system monospace font, but box drawing,
// blocks, braille and the status glyphs as geometry, so lines join without
// gaps and dots stay round on every platform and at every size. The
// providers' logos the TUI draws with a Nerd Font are drawn from their
// outlines, so the page needs no such font.

// The page's monospace stack (--mono), whose CJK fonts follow its language.
const FONT = getComputedStyle(document.documentElement).getPropertyValue('--mono').trim()
  || 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, "Cascadia Mono", "DejaVu Sans Mono", "Noto Sans Mono", monospace, sans-serif';
const TEXT = '#d5dee7';
const CONT = '\u0001'; // the right half of a wide character

// Box drawing: arms up, right, down, left; 1 light, 2 heavy.
const BOX = {
  '─': [0, 1, 0, 1], '━': [0, 2, 0, 2], '│': [1, 0, 1, 0], '┃': [2, 0, 2, 0],
  '┌': [0, 1, 1, 0], '┐': [0, 0, 1, 1], '└': [1, 1, 0, 0], '┘': [1, 0, 0, 1],
  '├': [1, 1, 1, 0], '┤': [1, 0, 1, 1], '┬': [0, 1, 1, 1], '┴': [1, 1, 0, 1], '┼': [1, 1, 1, 1],
  '╴': [0, 0, 0, 1], '╵': [1, 0, 0, 0], '╶': [0, 1, 0, 0], '╷': [0, 0, 1, 0],
};
const DASH = { '╌': [0, 2], '┄': [0, 3], '┈': [0, 4], '╎': [1, 2], '┆': [1, 3], '┊': [1, 4] };
const ARC = { '╭': [1, 1], '╮': [-1, 1], '╯': [-1, -1], '╰': [1, -1] };

// The logos, as a Nerd Font (v3.5.1, nerdfonts.com) draws them, by code
// point: their bounds and outline in its units, 2048 to the em, y up. They
// are Devicons' (devicon.dev, MIT) but Linode's, which is Font Awesome's
// (fontawesome.com, CC BY 4.0), and each is its owner's trademark. Oracle's
// is an oval ring from Material Design Icons (pictogrammers.com, Apache
// 2.0) after the ring of its mark: the fonts' Oracle logo is its wordmark,
// a line at the size of a letter.
const LOGOS = {
  0xe792: [0, 255, 1587, 973, 'M1091 294 1083 265Q1080 255 1070 255H13Q4 255 2 265Q0 289 0 300Q0 398 68 468Q135 537 233 540Q226 567 229 599Q235 659 278 702Q320 744 380 750Q386 751 397 751Q454 751 499 717Q537 830 634 902Q732 973 855 973Q989 973 1092 890Q1194 806 1222 679Q1223 676 1221 668L1188 581Q1169 530 1120 494Q1070 457 1019 455L442 447Q431 447 428 437Q426 431 429 427Q432 422 438 422L1011 415Q1058 413 1081 381Q1106 345 1091 294ZM1274 661H1258Q1252 661 1250 655L1227 577Q1212 526 1237 490Q1261 457 1308 455L1430 448Q1435 448 1438 443Q1442 438 1440 433Q1437 423 1426 423L1299 415Q1247 413 1198 376Q1149 340 1130 289L1121 265Q1120 261 1122 258Q1124 256 1128 256H1564Q1573 256 1575 264Q1587 310 1587 349Q1587 478 1496 570Q1404 661 1274 661Z'], // cloudflare
  0xe8d3: [0, -96, 1638, 1324, 'M819 1324 1638 -96H0ZM819 1324Z'], // vercel
  0xe8b6: [2, -205, 1596, 1433, 'M723 1433Q750 1432 770 1412Q791 1392 792 1362L796 850H1456Q1499 850 1534 826Q1568 803 1584 768Q1600 734 1596 692Q1591 651 1562 617L931 -179Q914 -205 878 -205Q846 -205 826 -185Q805 -165 805 -132L798 378H145Q101 378 66 401Q32 424 16 458Q-1 493 2 534Q5 576 33 610L665 1406Q685 1433 720 1433ZM858 783 871 -132Q871 -135 872 -137Q874 -139 876 -139H878H884L1515 657Q1525 673 1528 694Q1530 715 1522 737Q1503 783 1456 783Z'], // supabase
  0xe7f1: [1, 15, 1501, 1213, 'M757 1213Q863 1214 963 1176Q1063 1139 1144 1067V1063V1062Q1271 948 1315 785Q1431 707 1477 576Q1523 446 1482 313Q1440 180 1328 98Q1215 16 1074 17H767V16H430L432 15Q290 15 176 92L175 91Q62 176 20 307Q-21 438 24 569Q73 706 197 786Q239 931 344 1038Q448 1145 591 1188Q676 1213 757 1213ZM766 954Q699 955 636 929Q572 903 525 852Q607 838 678 796Q750 754 801 690L602 500Q578 551 528 577Q478 603 422 596Q365 588 324 548Q284 510 276 458Q267 406 291 359Q315 312 364 287Q399 274 430 274H749V276H1074Q1119 276 1154 294Q1189 312 1207 340Q1225 368 1234 402Q1242 437 1234 472Q1225 506 1207 534Q1189 562 1154 580Q1119 598 1074 598V630Q1077 704 1046 771Q1014 838 955 886L956 887Q870 952 766 954Z'], // gcp
  0xe754: [0, -146, 1617, 1374, 'M552 1374H1082Q1107 1374 1126 1360Q1145 1345 1153 1322L1613 -40Q1617 -50 1617 -64Q1617 -89 1603 -108Q1579 -140 1541 -140H1012Q1043 -140 1065 -118Q1087 -95 1087 -64Q1087 -50 1083 -40L624 1322Q616 1345 596 1360Q577 1374 552 1374ZM535 1368Q560 1368 578 1356Q589 1349 598 1340Q606 1330 607 1326L805 742L689 408H468Q444 408 430 388Q423 377 424 365Q426 353 430 346Q433 338 435 336L607 172L517 -94Q509 -118 489 -132Q469 -146 445 -146H75Q44 -146 22 -124Q0 -101 0 -70Q0 -54 3 -46L463 1316Q471 1339 490 1354Q510 1368 535 1368ZM472 398H926L1073 -40Q1082 -79 1063 -104Q1055 -112 1040 -122Q1025 -133 1010 -134H984Q955 -134 933 -114L449 338Q437 348 437 364Q437 378 448 388Q458 398 472 398Z'], // azure
  0xe7ad: [1, 130, 1622, 1098, 'M1382 1098H1383Q1396 1098 1418 1096Q1442 1092 1453 1090Q1466 1088 1484 1082Q1495 1079 1506 1072Q1519 1066 1523 1058Q1527 1053 1527 1042V1016Q1527 998 1515 998Q1511 998 1495 1006Q1449 1026 1391 1026Q1347 1026 1319 1010Q1294 996 1294 962Q1294 939 1310 924Q1328 909 1370 896L1446 870Q1505 852 1530 820Q1554 788 1554 744Q1554 710 1540 682Q1523 650 1501 634Q1476 614 1441 604Q1408 592 1364 592Q1322 592 1281 602Q1243 610 1219 624Q1205 631 1202 638Q1199 646 1199 654V682Q1199 698 1212 698Q1218 698 1221 696Q1223 696 1228 694Q1233 691 1235 690Q1268 677 1294 672Q1327 666 1358 666Q1410 666 1437 684Q1465 702 1465 734Q1465 758 1450 772Q1436 787 1396 800L1318 824Q1260 842 1232 880Q1205 916 1205 958Q1205 994 1219 1018Q1235 1044 1258 1062Q1285 1082 1314 1090Q1347 1098 1382 1098ZM271 1098Q368 1098 412 1054Q457 1011 457 922V746H458Q458 716 464 698Q474 672 481 660Q485 652 485 648Q485 639 474 632L440 608Q434 604 426 604Q417 604 410 612Q401 623 391 636Q381 653 374 668Q312 594 216 594Q147 594 108 634Q68 674 68 736Q68 806 117 848Q167 890 249 890Q280 890 306 886Q333 881 368 874V912Q368 974 343 1000Q319 1024 255 1024Q224 1024 197 1016Q169 1009 139 998Q128 993 120 992Q112 990 111 990Q100 990 100 1006V1034Q100 1046 104 1052Q110 1059 119 1064Q147 1078 187 1088Q224 1098 271 1098ZM521 1084H574Q590 1084 595 1080Q601 1075 606 1058L696 702L781 1058Q786 1075 791 1080Q798 1084 812 1084H855Q872 1084 877 1080Q884 1074 887 1058L972 698L1066 1058Q1071 1075 1076 1080Q1083 1084 1097 1084H1147Q1161 1084 1161 1072Q1161 1066 1160 1062Q1160 1061 1158 1057Q1157 1053 1156 1050L1026 632Q1022 616 1016 611Q1009 606 994 606H948Q932 606 927 612Q919 620 916 634L833 980L750 634Q747 620 739 612Q733 606 718 606H672Q657 606 651 612Q645 617 640 634L513 1050Q508 1068 508 1072Q508 1084 521 1084ZM269 826Q216 826 189 804Q162 781 162 742Q162 704 182 684Q202 664 239 664Q263 664 294 674Q324 685 346 710Q360 725 364 744Q369 765 369 790V814Q345 820 320 822Q295 826 269 826ZM1502 494Q1543 495 1575 488Q1607 482 1615 472Q1621 465 1622 443Q1622 421 1616 390Q1609 358 1597 324Q1585 291 1564 258Q1542 224 1515 202Q1504 193 1496 196Q1489 200 1493 212Q1565 384 1540 414Q1517 444 1332 422Q1319 420 1318 428Q1316 437 1328 446Q1395 493 1502 494ZM13 462Q21 462 26 460Q201 358 407 302Q613 246 828 246Q1144 246 1440 372Q1463 381 1476 364Q1488 347 1466 330Q1337 235 1160 182Q982 130 810 130Q582 130 376 210Q169 290 8 436Q-1 444 2 453Q4 462 13 462Z'], // aws
  0xf0ea1: [0, -70, 1704, 1298, 'M852 1126Q1035 1126 1194 1058Q1353 990 1444 872Q1536 753 1536 614Q1536 475 1444 356Q1353 238 1194 170Q1035 102 852 102Q669 102 510 170Q351 238 260 356Q168 475 168 614Q168 753 260 872Q351 990 510 1058Q669 1126 852 1126ZM852 1298Q622 1298 424 1206Q229 1115 114 956Q0 798 0 614Q0 430 114 272Q229 113 424 22Q622 -70 852 -70Q1082 -70 1280 22Q1475 113 1590 272Q1704 430 1704 614Q1704 798 1590 956Q1475 1115 1280 1206Q1082 1298 852 1298Z'], // oracle
  0xe7c0: [0, -180, 1587, 1408, 'M485 1345Q632 1408 793 1408Q954 1408 1101 1345Q1248 1282 1354 1176Q1461 1070 1524 923Q1587 776 1587 614Q1587 453 1524 306Q1461 159 1354 52Q1248 -54 1101 -117Q954 -180 793 -180Q632 -180 485 -117Q338 -54 232 52Q126 159 63 306Q0 453 0 614Q0 776 63 923Q126 1070 232 1176Q338 1282 485 1345ZM1204 1150Q1203 1148 1203 1142Q1203 1128 1212 1118Q1220 1108 1234 1104Q1237 1104 1242 1104Q1254 1104 1264 1112Q1275 1119 1279 1130V1132L1280 1136Q1256 1159 1219 1186L1214 1190L1204 1152ZM1027 1078 991 946Q1030 927 1057 906L1060 904L1102 1056V1058Q1106 1072 1098 1086Q1089 1100 1074 1104Q1059 1108 1045 1100Q1031 1093 1027 1078ZM889 1248Q888 1244 888 1240Q888 1226 896 1216Q905 1206 919 1202Q922 1202 927 1202Q940 1202 950 1209Q960 1216 964 1228L984 1302Q940 1313 913 1318H908ZM601 1130V1122Q601 1108 609 1098Q617 1088 631 1084Q634 1084 639 1084Q652 1084 662 1091Q672 1098 676 1110L729 1306V1308Q732 1315 730 1324Q696 1322 656 1314H651L602 1132ZM151 712H152L204 908H205Q206 912 206 918Q206 932 198 942Q190 952 177 956Q174 956 168 956Q101 836 85 698Q93 688 104 686Q119 682 133 690Q147 697 151 712ZM310 606Q314 620 306 634Q298 647 283 652Q268 655 254 648Q239 640 235 626V624L182 430V428Q178 413 186 399Q195 385 211 382Q226 378 240 386Q253 393 257 408L310 604ZM325 872Q340 868 354 876Q367 883 371 898H372L424 1094H425Q426 1098 426 1104Q426 1117 418 1128Q410 1139 397 1142Q382 1145 368 1137Q354 1129 350 1114L297 918Q293 902 301 888Q309 875 325 872ZM427 204 475 382Q435 403 408 428L406 422L405 420L353 224H352Q348 208 356 194Q365 180 381 176Q396 173 409 180Q422 188 427 204ZM587 84Q591 100 582 114Q574 128 559 132Q556 132 549 132Q536 132 526 124Q516 117 512 104L477 -24Q509 -41 544 -54L549 -56ZM804 184V186L803 192L801 200L800 206Q793 221 776 226Q770 228 765 228Q752 228 742 220Q732 212 728 200V198L675 4V2Q674 0 674 -6Q674 -20 682 -30Q691 -40 705 -44Q708 -44 713 -44Q726 -44 736 -36Q746 -29 750 -18V-16L803 178V180ZM1319 134 1323 136 1307 196 1265 352 1239 450 1216 536 1202 588 1193 620 1188 640 1183 658 1181 664 1179 668V672H1178Q1141 793 1043 870Q930 958 762 958Q648 958 554 915Q460 872 406 797Q352 722 352 632Q352 505 466 430Q581 355 773 358H781L809 360L802 342L803 340Q804 337 806 332Q807 327 808 324L809 322L810 320L811 316L812 312L813 310L814 306L816 302L817 296L818 292L820 286L821 282L822 276L824 268L825 264L827 258L828 250L830 242L831 236L833 230L834 222L836 214L838 202L840 192L843 180L844 174L846 162L848 152L851 138L853 124L856 108L859 92L861 76L864 60L867 42L869 28L873 6L874 -4L878 -28L881 -46L883 -62L886 -82L887 -92Q1013 -75 1124 -16Q1236 42 1319 134ZM1358 908Q1359 911 1359 918Q1359 931 1352 942Q1344 952 1331 956Q1328 956 1321 956Q1308 956 1298 949Q1287 942 1283 930V928L1231 734L1230 732Q1226 717 1234 703Q1243 689 1259 686Q1274 682 1288 690Q1301 697 1305 712ZM1492 648V650Q1493 652 1493 658Q1493 672 1485 683Q1477 694 1464 696Q1449 700 1435 692Q1421 684 1417 670V668L1364 474V472Q1360 457 1368 443Q1375 429 1391 424Q1407 420 1420 428Q1434 437 1439 452V454ZM846 814Q861 799 861 778Q861 758 846 744Q832 730 812 730Q792 730 777 744Q762 758 762 778Q762 799 777 814Q792 828 812 828Q832 828 846 814Z'], // deno
  0xf2b8: [0, -331, 1574, 1559, 'M1312 869 1574 724 1358 563 1333 288 1240 353 1250 486 1151 418 977 294 980 9 857 114 854 204 798 164 510 -40Q513 -56 553 -331L309 -74L247 217L325 263L216 365L139 739L256 795L102 916L0 1414L464 1559L801 1398L325 1216L402 687L680 829L829 733L430 504L485 118L844 359V361Q842 404 835 579L980 668L1089 597L1092 733Z'], // linode
  0xe7ae: [0, -202, 1639, 1430, 'M821 115H502V428H821ZM502 -130H261V115H502ZM261 115H58V317H261ZM816 -202V115Q899 115 977 142Q1055 168 1117 214Q1179 260 1226 326Q1272 391 1296 465Q1321 539 1319 626Q1317 713 1285 798Q1249 897 1174 972Q1100 1047 1000 1082Q915 1113 829 1114Q743 1115 668 1091Q594 1067 529 1020Q464 974 418 912Q372 851 346 774Q319 698 319 616H0Q0 714 24 808Q47 903 90 985Q132 1067 194 1139Q255 1211 329 1266Q403 1322 492 1360Q580 1399 674 1416Q769 1434 874 1428Q978 1422 1082 1390Q1267 1332 1404 1196Q1541 1059 1599 875Q1631 772 1637 668Q1643 563 1626 469Q1608 375 1569 287Q1530 199 1474 126Q1418 52 1345 -10Q1272 -71 1189 -113Q1106 -155 1010 -178Q915 -202 816 -202ZM816 -202Z'], // digitalocean
};

const isLogo = (chr) => chr !== undefined && LOGOS[chr.codePointAt(0)] !== undefined;

const hex = (n) => '#' + n.toString(16).padStart(6, '0');

export class Screen {
  constructor(canvas, cols, rows, styles, cellAspect = 2) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d', { alpha: false });
    this.cellAspect = cellAspect;
    this.reset(cols, rows, styles);
  }

  reset(cols, rows, styles) {
    this.cols = cols;
    this.rows = rows;
    this.chars = new Array(cols * rows).fill(' ');
    this.style = new Int32Array(cols * rows);
    this.styles = [];
    this.addStyles(styles);
    this.dirty = new Uint8Array(cols * rows).fill(1);
    this.cw = 0;
    this.resize();
  }

  addStyles(styles) {
    this.styles.push(...styles.map(([fg, bg, attr]) => ({
      fg: fg < 0 ? TEXT : hex(fg), bg: hex(bg), bold: !!(attr & 1), italic: !!(attr & 4), underline: !!(attr & 8),
    })));
  }

  patch(frame) {
    if (frame.reset) this.reset(...frame.size, frame.styles);
    else this.addStyles(frame.styles);
    this.apply(frame.ops);
  }

  // Match the cell shape the Go renderer uses to keep maps and globes round.
  resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 3);
    const cssW = this.canvas.clientWidth || this.canvas.parentElement.clientWidth;
    const w = Math.max(1, Math.round(cssW * dpr));
    const cw = w / this.cols;
    const h = Math.round(cw * this.cellAspect * this.rows);
    if (w === this.canvas.width && h === this.canvas.height && cw === this.cw) return false;
    this.canvas.width = w;
    this.canvas.height = h;
    this.cw = cw;
    this.ch = cw * this.cellAspect;
    const c = this.ctx;
    c.font = `100px ${FONT}`;
    const adv = c.measureText('M').width / 100 || 0.6;
    this.px = Math.max(4, Math.min(this.cw / adv, this.ch * 0.86));
    c.font = `${this.px}px ${FONT}`;
    const m = c.measureText('H');
    this.capH = m.actualBoundingBoxAscent || this.px * 0.7;
    this.line = Math.max(1, Math.round(this.cw * 0.11));
    this.fontKey = '';
    this.dirty.fill(1);
    return true;
  }

  // apply writes one recorded frame: [y, x, text, [style, count, ...]].
  apply(ops) {
    for (const [y, x0, text, runs] of ops) {
      let i = y * this.cols + x0;
      let r = 0, left = runs[1];
      for (const chr of text) {
        this.chars[i] = chr;
        this.style[i] = runs[r];
        this.dirty[i] = 1;
        i++;
        if (--left === 0) { r += 2; left = runs[r + 1]; }
      }
    }
  }

  paint() {
    const { cols, rows, chars, dirty } = this;
    for (let y = 0; y < rows; y++) {
      for (let x = 0; x < cols; x++) {
        const i = y * cols + x;
        if (!dirty[i]) continue;
        let k = x;
        // A dirty right half repaints its wide character, and the space
        // after a logo the logo, which spreads over it.
        if (x > 0 && (chars[i] === CONT || chars[i] === ' ' && isLogo(chars[i - 1]))) k = x - 1;
        this.cell(k, y);
        dirty[i] = 0;
        const next = y * cols + k + 1;
        if (k + 1 < cols && (chars[next] === CONT || chars[next] === ' ' && isLogo(chars[next - 1]))) dirty[next] = 0;
      }
    }
  }

  // Cell edges are rounded to device pixels so neighbours meet exactly.
  edges(x, y, span = 1) {
    return [Math.round(x * this.cw), Math.round(y * this.ch), Math.round((x + span) * this.cw), Math.round((y + 1) * this.ch)];
  }

  cell(x, y) {
    const i = y * this.cols + x;
    const chr = this.chars[i];
    const st = this.styles[this.style[i]];
    const wide = x + 1 < this.cols && this.chars[i + 1] === CONT;
    const [x0, y0, x1, y1] = this.edges(x, y, wide ? 2 : 1);
    const c = this.ctx;
    c.fillStyle = st.bg;
    c.fillRect(x0, y0, x1 - x0, y1 - y0);
    if (chr === ' ' || chr === CONT) return;
    c.fillStyle = st.fg;
    c.strokeStyle = st.fg;
    const code = chr.codePointAt(0);
    if (code >= 0x2800 && code <= 0x28ff) return this.braille(code - 0x2800, x0, y0, x1, y1);
    if (LOGOS[code]) return this.logo(LOGOS[code], x, y, x0, y0, x1, y1);
    if (this.geometry(chr, x0, y0, x1, y1, st)) return;
    const key = (st.italic ? 'italic ' : '') + (st.bold ? '600 ' : '400 ');
    if (key !== this.fontKey) {
      c.font = `${key}${this.px}px ${FONT}`;
      this.fontKey = key;
    }
    c.textAlign = 'center';
    c.textBaseline = 'alphabetic';
    c.fillText(chr, (x0 + x1) / 2, (y0 + y1) / 2 + this.capH / 2, (x1 - x0) * 1.15);
    if (st.underline) c.fillRect(x0, Math.round((y0 + y1) / 2 + this.capH / 2 + this.line * 2), x1 - x0, this.line);
  }

  // logo draws a provider's logo as a Nerd Font would, about a letter and a
  // half wide from the left of its cell into the space after it, centred on
  // the line.
  logo([bx0, by0, bx1, by1, d], x, y, x0, y0, x1, y1) {
    const c = this.ctx;
    const i = y * this.cols + x;
    if (x + 1 < this.cols && this.chars[i + 1] === ' ') {
      const [, , x2] = this.edges(x + 1, y);
      c.fillStyle = this.styles[this.style[i + 1]].bg;
      c.fillRect(x1, y0, x2 - x1, y1 - y0);
      c.fillStyle = this.styles[this.style[i]].fg;
      x1 = x2;
    }
    const inset = Math.max(0.5, this.cw * 0.06);
    const s = Math.min(this.px / 2048, (x1 - x0 - 3 * inset) / (bx1 - bx0), (y1 - y0) * 0.8 / (by1 - by0));
    if (!this.paths) this.paths = new Map();
    let path = this.paths.get(d);
    if (!path) this.paths.set(d, path = new Path2D(d));
    c.save();
    c.translate(x0 + inset - bx0 * s, (y0 + y1) / 2 + (by0 + by1) / 2 * s);
    c.scale(s, -s);
    c.fill(path);
    c.restore();
  }

  braille(bits, x0, y0, x1, y1) {
    const c = this.ctx;
    const w = x1 - x0, h = y1 - y0;
    const r = Math.max(0.6, Math.min(w / 2, h / 4) * 0.36);
    // Dot order: 1 2 3 7 down the left column, 4 5 6 8 down the right.
    const pos = [[0, 0], [0, 1], [0, 2], [1, 0], [1, 1], [1, 2], [0, 3], [1, 3]];
    c.beginPath();
    for (let b = 0; b < 8; b++) {
      if (!(bits & (1 << b))) continue;
      const [cx, cy] = pos[b];
      const px = x0 + w * (cx * 0.5 + 0.25), py = y0 + h * (cy * 0.25 + 0.125);
      c.moveTo(px + r, py);
      c.arc(px, py, r, 0, Math.PI * 2);
    }
    c.fill();
  }

  // geometry draws the glyphs a font would get wrong; false leaves the
  // character to the font.
  geometry(chr, x0, y0, x1, y1) {
    const c = this.ctx;
    const w = x1 - x0, h = y1 - y0;
    const t = this.line;
    // Line centres, aligned to the pixel grid identically in every cell.
    const vx = x0 + Math.floor((w - t) / 2), hy = y0 + Math.floor((h - t) / 2);
    const box = BOX[chr];
    if (box) {
      const [u, r, d, l] = box;
      const th = (k) => (k === 2 ? t * 2 : t);
      const off = (k) => (k === 2 ? Math.floor(t / 2) : 0);
      const wide = Math.max(th(u), th(d)), tall = Math.max(th(l), th(r));
      if (u) c.fillRect(vx - off(u), y0, th(u), hy - y0 + tall);
      if (d) c.fillRect(vx - off(d), hy, th(d), y1 - hy);
      if (l) c.fillRect(x0, hy - off(l), vx - x0 + wide, th(l));
      if (r) c.fillRect(vx, hy - off(r), x1 - vx, th(r));
      return true;
    }
    const dash = DASH[chr];
    if (dash) {
      const [vertical, n] = dash;
      const len = (vertical ? h : w) / n;
      for (let k = 0; k < n; k++) {
        const a = Math.round(k * len + len * 0.2), b = Math.round(k * len + len * 0.8);
        if (vertical) c.fillRect(vx, y0 + a, t, b - a);
        else c.fillRect(x0 + a, hy, b - a, t);
      }
      return true;
    }
    const arc = ARC[chr];
    if (arc) {
      const [sx, sy] = arc;
      const cx = vx + t / 2, cy = hy + t / 2;
      const ex = sx > 0 ? x1 : x0, ey = sy > 0 ? y1 : y0;
      const r = Math.min(Math.abs(ex - cx), Math.abs(ey - cy));
      c.lineWidth = t;
      c.lineCap = 'butt';
      c.beginPath();
      c.moveTo(ex + sx, cy);
      c.lineTo(cx + sx * r, cy);
      c.arcTo(cx, cy, cx, cy + sy * r, r);
      c.lineTo(cx, ey + sy);
      c.stroke();
      return true;
    }
    const mid = y0 + Math.round(h / 2), cx = (x0 + x1) / 2, cy = (y0 + y1) / 2;
    const s = Math.min(w, h / 2);
    switch (chr) {
      case '█': c.fillRect(x0, y0, w, h); return true;
      case '▀': c.fillRect(x0, y0, w, mid - y0); return true;
      case '▄': c.fillRect(x0, mid, w, y1 - mid); return true;
      case '▌': c.fillRect(x0, y0, Math.round(w / 2), h); return true;
      case '▐': c.fillRect(x0 + Math.round(w / 2), y0, w - Math.round(w / 2), h); return true;
      case '▎': c.fillRect(x0, y0, Math.max(1, Math.round(w / 4)), h); return true;
      case '▏': c.fillRect(x0, y0, Math.max(1, Math.round(w / 8)), h); return true;
      case '╱':
        c.lineWidth = t;
        c.beginPath(); c.moveTo(x1, y0); c.lineTo(x0, y1); c.stroke();
        return true;
      case '●': disc(c, cx, cy, s * 0.36); return true;
      case '○': ring(c, cx, cy, s * 0.32, t); return true;
      case '◉': ring(c, cx, cy, s * 0.38, t); disc(c, cx, cy, s * 0.2); return true;
      case '◎': ring(c, cx, cy, s * 0.38, t); ring(c, cx, cy, s * 0.17, t); return true;
      case '■': c.fillRect(cx - s * 0.3, cy - s * 0.3, s * 0.6, s * 0.6); return true;
      case '◆': poly(c, [[cx, cy - s * 0.42], [cx + s * 0.38, cy], [cx, cy + s * 0.42], [cx - s * 0.38, cy]]); return true;
      case '▲': poly(c, [[cx, cy - s * 0.38], [cx + s * 0.42, cy + s * 0.32], [cx - s * 0.42, cy + s * 0.32]]); return true;
      case '▸': poly(c, [[cx - s * 0.24, cy - s * 0.3], [cx + s * 0.3, cy], [cx - s * 0.24, cy + s * 0.3]]); return true;
      case '◂': poly(c, [[cx + s * 0.24, cy - s * 0.3], [cx - s * 0.3, cy], [cx + s * 0.24, cy + s * 0.3]]); return true;
      case '✖': {
        const a = s * 0.3;
        c.lineWidth = Math.max(t * 1.6, s * 0.16);
        c.lineCap = 'round';
        c.beginPath();
        c.moveTo(cx - a, cy - a); c.lineTo(cx + a, cy + a);
        c.moveTo(cx + a, cy - a); c.lineTo(cx - a, cy + a);
        c.stroke();
        return true;
      }
      case '✓': {
        c.lineWidth = Math.max(t * 1.4, s * 0.12);
        c.lineCap = 'round';
        c.lineJoin = 'round';
        c.beginPath();
        c.moveTo(cx - s * 0.32, cy); c.lineTo(cx - s * 0.08, cy + s * 0.26); c.lineTo(cx + s * 0.34, cy - s * 0.3);
        c.stroke();
        return true;
      }
      case '⚑': {
        c.fillRect(cx - s * 0.3, cy - s * 0.42, Math.max(1, t), s * 0.86);
        poly(c, [[cx - s * 0.3, cy - s * 0.42], [cx + s * 0.36, cy - s * 0.24], [cx - s * 0.3, cy - s * 0.02]]);
        return true;
      }
    }
    return false;
  }
}

function disc(c, x, y, r) {
  c.beginPath();
  c.arc(x, y, r, 0, Math.PI * 2);
  c.fill();
}

function ring(c, x, y, r, t) {
  c.lineWidth = t;
  c.beginPath();
  c.arc(x, y, r, 0, Math.PI * 2);
  c.stroke();
}

function poly(c, pts) {
  c.beginPath();
  c.moveTo(pts[0][0], pts[0][1]);
  for (const [x, y] of pts.slice(1)) c.lineTo(x, y);
  c.closePath();
  c.fill();
}
