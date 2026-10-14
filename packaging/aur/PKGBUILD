pkgname=bors
pkgver=0.1.0
pkgrel=1
pkgdesc='Display images in terminals using Kitty Graphics Protocol or ANSI blocks'
arch=('x86_64' 'aarch64')
url='https://github.com/kristyancarvalho/bors'
license=('MIT')
makedepends=('git' 'go>=1.27.2' 'meson>=1.2.0' 'ninja')
source=("git+https://github.com/kristyancarvalho/bors.git#tag=v${pkgver}")
sha256sums=('SKIP')

build() {
  cd "$srcdir/bors"
  meson setup build --prefix=/usr --buildtype=release
  meson compile -C build
}

check() {
  cd "$srcdir/bors"
  meson test -C build --print-errorlogs
}

package() {
  cd "$srcdir/bors"
  DESTDIR="$pkgdir" meson install -C build
  install -Dm644 LICENSE "$pkgdir/usr/share/licenses/$pkgname/LICENSE"
}
