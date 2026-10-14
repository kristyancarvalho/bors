project(
  'bors',
  version: '0.1.0',
  meson_version: '>=1.2.0',
)

go = find_program('go')
source_root = meson.project_source_root()
go_sources = files(
  'cmd/bors/main.go',
  'internal/cli/cli.go',
  'internal/cli/options.go',
  'internal/input/gif.go',
  'internal/input/image.go',
  'internal/render/ansi/ansi.go',
  'internal/render/kitty/kitty.go',
  'internal/render/size/size.go',
  'internal/terminal/probe.go',
  'internal/terminal/terminal.go',
  'tests/bors_test.go',
)
linker_flags = '-X=main.version=' + meson.project_version()
if get_option('buildtype') == 'release'
  linker_flags = '-s -w ' + linker_flags
  if host_machine.system() == 'linux'
    linker_flags = '-linkmode=external -extldflags=-Wl,-z,relro,-z,now ' + linker_flags
  endif
endif

bors = custom_target(
  'bors',
  output: 'bors',
  command: [go, '-C', source_root, 'build', '-buildmode=pie', '-trimpath', '-ldflags=' + linker_flags, '-o', meson.project_build_root() / 'bors', './cmd/bors'],
  depend_files: go_sources + files('go.mod', 'go.sum'),
  build_by_default: true,
  install: true,
  install_dir: get_option('bindir'),
)

test('go-test', go, args: ['-C', source_root, 'test', './...'], workdir: source_root, suite: 'unit', timeout: 300)
test('go-race', go, args: ['-C', source_root, 'test', '-race', './...'], workdir: source_root, suite: 'race', timeout: 300)
test('go-vet', go, args: ['-C', source_root, 'vet', './...'], workdir: source_root, suite: 'quality', timeout: 300)
test('go-build', go, args: ['-C', source_root, 'build', './...'], workdir: source_root, suite: 'quality', timeout: 300)

run_args = get_option('run_args')
run_target('run', command: [bors] + run_args)
