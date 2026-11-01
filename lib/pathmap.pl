#!/usr/bin/perl
# 路径换算。由 bin/ 下的脚本调用，配置从环境变量 MOUNT、PC_ROOT、HOME 读取。
#   check   <cwd>       命令从标准输入读。命令里用到了挂载目录就返回 0（要转发），否则返回 1
#   rewrite <cwd>       命令从标准输入读。把命令里的 VPS 挂载路径换成电脑上的路径后输出；
#                       用到只在 VPS 上存在的文件时，在标准错误输出提示
#   filter-out          把电脑输出里的电脑路径换成 VPS 挂载路径，用于标准输出
#   filter-err <file>   同上，用于标准错误；另外取出电脑上命令结束时的目录，换算后写入 <file>
# PC_BRIDGE_RAW_OUTPUT=1 时 filter-* 不换算路径（filter-err 仍会取出目录）。
use strict;
use warnings;

my $mode = shift // die "用法: pathmap.pl check|rewrite|filter-out|filter-err ...\n";
my $mount = $ENV{MOUNT} // die "缺少 MOUNT\n";
my $pc_root = $ENV{PC_ROOT} // die "缺少 PC_ROOT\n";
my $home = $ENV{HOME} // '';
$mount =~ s{/+$}{};
$pc_root =~ s{/+$}{};

# 电脑是 Windows 时 PC_ROOT 形如 /d/Code：盘符和路径都不区分大小写，输出里还可能是 D:\Code、D:/Code
my ($drive, $win_rest) = $pc_root =~ m{^/([A-Za-z])(/.*)?$};
$win_rest //= '';

# ---- 命令里的路径：VPS → 电脑 ----

# 挂载目录在命令里的几种写法，长的在前
my @aliases = ($mount);
if ($home ne '' && index("$mount/", "$home/") == 0) {
    my $rel = substr($mount, length $home);
    push @aliases, "~$rel", "\$HOME$rel", "\${HOME}$rel";
}
@aliases = sort { length $b <=> length $a } @aliases;

# 路径前后的边界：前面不能紧挨路径字符，后面只能是 / 或分隔符，避免 pc-code-old 这类目录被误认
my $before = qr{(?<![\w.\-/~\$\{])};
my $after = qr{(?=[/\s'"`;|&()<>:,=]|$)};

sub replace_aliases {
    my ($cmd) = @_;
    my $n = 0;
    for my $a (@aliases) {
        $n += ($cmd =~ s{$before\Q$a\E$after}{$pc_root}g) || 0;
    }
    return ($cmd, $n);
}

sub under_mount {
    my ($p) = @_;
    return $p eq $mount || index($p, "$mount/") == 0;
}

# 把 a/./b/../c 这类路径规范化，不访问文件系统（挂载目录在 sshfs 上，访问很慢）
sub normalize {
    my ($p) = @_;
    my @out;
    for my $part (split m{/}, $p) {
        next if $part eq '' || $part eq '.';
        if ($part eq '..') { pop @out; next }
        push @out, $part;
    }
    return '/' . join('/', @out);
}

# 按 shell 规则把命令拆成单词。每个单词记下在原文里的位置、去掉引号后的值，以及有没有引号、变量、转义
sub split_words {
    my ($s) = @_;
    my @words;
    my $i = 0;
    my $len = length $s;
    while ($i < $len) {
        my $c = substr($s, $i, 1);
        if ($c =~ /\s/ || $c =~ /[;|&()<>]/) { $i++; next }
        if ($c eq '#') {    # 注释到行尾
            $i++ while $i < $len && substr($s, $i, 1) ne "\n";
            next;
        }
        my $start = $i;
        my ($value, $quoted, $dynamic) = ('', 0, 0);
        while ($i < $len) {
            $c = substr($s, $i, 1);
            last if $c =~ /\s/ || $c =~ /[;|&()<>]/;
            if ($c eq "'") {
                my $end = index($s, "'", $i + 1);
                $end = $len if $end < 0;
                $value .= substr($s, $i + 1, $end - $i - 1);
                $quoted = 1;
                $i = $end + 1;
            } elsif ($c eq '"') {
                $quoted = 1;
                $i++;
                while ($i < $len && substr($s, $i, 1) ne '"') {
                    my $d = substr($s, $i, 1);
                    if ($d eq '\\' && $i + 1 < $len) { $dynamic = 1; $value .= substr($s, $i + 1, 1); $i += 2; next }
                    $dynamic = 1 if $d eq '$' || $d eq '`';
                    $value .= $d;
                    $i++;
                }
                $i++;
            } elsif ($c eq '\\') {
                $dynamic = 1;
                $value .= substr($s, $i + 1, 1) if $i + 1 < $len;
                $i += 2;
            } else {
                $dynamic = 1 if $c eq '$' || $c eq '`';
                $value .= $c;
                $i++;
            }
        }
        push @words, { start => $start, len => $i - $start, value => $value, quoted => $quoted, dynamic => $dynamic };
    }
    return @words;
}

# 单词里可能是路径的部分：整个单词，或者 --opt=值 里等号后面的值
sub path_part {
    my ($w) = @_;
    my $v = $w->{value};
    return (0, $v) unless $v =~ /^-/;
    my $eq = index($v, '=');
    return $eq < 0 ? () : ($eq + 1, substr($v, $eq + 1));
}

# 在 cwd 不在挂载目录时，找出解析后落在挂载目录里的相对路径。返回 [单词, 偏移, 电脑路径]
sub relative_hits {
    my ($cmd, $cwd) = @_;
    return () if under_mount($cwd);
    my @hits;
    for my $w (split_words($cmd)) {
        next if $w->{dynamic};
        my ($off, $p) = path_part($w);
        next if !defined $p || $p eq '' || $p =~ m{^[/~\$]};
        my $abs = normalize("$cwd/$p");
        next unless under_mount($abs);
        push @hits, [$w, $off, $pc_root . substr($abs, length $mount), $p];
    }
    return @hits;
}

sub read_stdin {
    local $/;
    my $s = <STDIN>;
    return $s // '';
}

if ($mode eq 'check') {
    my $cwd = shift // '';
    my $cmd = read_stdin();
    exit 0 if under_mount($cwd);
    my (undef, $n) = replace_aliases($cmd);
    exit 0 if $n;
    exit(relative_hits($cmd, $cwd) ? 0 : 1);
}

if ($mode eq 'rewrite') {
    my $cwd = shift // '';
    my $cmd = read_stdin();
    ($cmd) = replace_aliases($cmd);

    # 相对路径从后往前替换，前面单词的位置不受影响
    for my $h (reverse relative_hits($cmd, $cwd)) {
        my ($w, $off, $pc, $p) = @$h;
        my $raw = substr($cmd, $w->{start}, $w->{len});
        my $new;
        if (!$w->{quoted}) {
            # 原文就是值本身，保留其中的通配符，只换路径
            $new = substr($raw, 0, $off) . $pc . substr($raw, $off + length $p);
        } elsif ($raw =~ /^(['"])(.*)\1$/s && $2 eq $w->{value}) {
            $new = $1 . substr($w->{value}, 0, $off) . $pc . substr($w->{value}, $off + length $p) . $1;
        } else {
            next;    # 引号只包住一部分的写法太复杂，不换
        }
        substr($cmd, $w->{start}, $w->{len}) = $new;
    }

    # 只在 VPS 上存在的文件：$HOME 或 /tmp 下、不在挂载目录里、VPS 上确实存在
    my %seen;
    for my $w (split_words($cmd)) {
        next if $w->{dynamic};
        my (undef, $p) = path_part($w);
        next if !defined $p || $p !~ m{^/[^/]+/[^/]};
        next if index("$p/", "$pc_root/") == 0 || under_mount($p);
        next unless ($home ne '' && index($p, "$home/") == 0) || index($p, '/tmp/') == 0;
        next if $seen{$p}++ || !-e $p;
        print STDERR "claude-pc-bridge 提示: $p 是 VPS 上的文件，电脑上没有。"
            . "要在 VPS 上处理它，在命令开头加 #local；要给电脑上的命令用，先把它放到挂载目录 $mount 里。\n";
    }
    print $cmd;
    exit 0;
}

# ---- 输出里的路径：电脑 → VPS ----

my $raw_output = ($ENV{PC_BRIDGE_RAW_OUTPUT} // '') eq '1';
my $end = qr{(?=[/\\\s'"`:;,)\]>]|$)};
my ($posix_re, $win_re);
if (defined $drive) {
    $posix_re = qr{(?<![\w.\-/])/\Q$drive$win_rest\E$end}i;
    my $win_tail = join('[\\\\/]+', map { quotemeta } grep { $_ ne '' } split m{/}, $win_rest);
    # D:\Code\proj\a.py、D:/Code/proj、Python repr 里的 D:\\Code\\proj；路径部分遇到空白、引号、冒号为止
    $win_re = qr{(?<![\w.\-/])\Q$drive\E:[\\/]+$win_tail((?:[\\/]+[^\s'"`:*?<>|\\/]+)*[\\/]*)$end}i;
} else {
    $posix_re = qr{(?<![\w.\-/])\Q$pc_root\E$end};
}

sub to_vps_line {
    my ($line) = @_;
    return $line if $raw_output;
    $line =~ s{$win_re}{my $t = $1; $t =~ s{[\\/]+}{/}g; $mount . $t}ge if $win_re;
    $line =~ s{$posix_re}{$mount}g;
    return $line;
}

# 电脑上命令结束时的目录（Git Bash 形式，如 /d/Code/proj）换算成挂载路径；不在 PC_ROOT 下时返回空
sub cwd_to_vps {
    my ($p) = @_;
    my $prefix = substr($p, 0, length $pc_root);
    my $same = defined $drive ? lc($prefix) eq lc($pc_root) : $prefix eq $pc_root;
    return '' unless $same;
    my $rest = substr($p, length $pc_root);
    return '' unless $rest eq '' || $rest =~ m{^/};
    return $mount . $rest;
}

if ($mode eq 'filter-out' || $mode eq 'filter-err') {
    my $cwd_file = $mode eq 'filter-err' ? (shift // '') : '';
    binmode STDIN;
    binmode STDOUT;
    $| = 1;
    while (my $line = <STDIN>) {
        if ($mode eq 'filter-err') {
            my $at = index($line, "\x1ePCB_CWD:");
            if ($at >= 0) {
                my $dir = substr($line, $at + 9);
                $dir =~ s/\r?\n$//;
                $line = substr($line, 0, $at);
                if ($cwd_file ne '') {
                    my $vps = cwd_to_vps($dir);
                    if (open my $fh, '>', $cwd_file) { print $fh $vps; close $fh }
                }
                next if $line eq '';
            }
        }
        print to_vps_line($line);
    }
    exit 0;
}

die "未知模式: $mode\n";
