//go:build windows

package netwatch

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rawChanges recebe os avisos dos callbacks. Callbacks do Go nunca são
// liberados: criamos um só, na inicialização. Por isso só deve haver UM
// Watcher por processo (todos compartilhariam este canal). Changes() entrega
// notificações brutas; o consumidor aplica Debounce.
var (
	rawChanges     = make(chan struct{}, 1)
	changeCallback = windows.NewCallback(func(callerCtx, row, notificationType uintptr) uintptr {
		select {
		case rawChanges <- struct{}{}:
		default:
		}
		return 0
	})
)

type winWatcher struct {
	once         sync.Once
	addr, routes windows.Handle
}

// New registra NotifyUnicastIpAddressChange e NotifyRouteChange2.
func New() (Watcher, error) {
	w := &winWatcher{}
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, changeCallback, nil, false, &w.addr); err != nil {
		return nil, err
	}
	if err := windows.NotifyRouteChange2(windows.AF_UNSPEC, changeCallback, nil, false, &w.routes); err != nil {
		_ = windows.CancelMibChangeNotify2(w.addr)
		return nil, err
	}
	return w, nil
}

func (w *winWatcher) Changes() <-chan struct{} { return rawChanges }

func (w *winWatcher) HasNetwork(exclude []string) (bool, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return false, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var routes []Route
	for _, r := range table.Rows() {
		if r.DestinationPrefix.PrefixLength != 0 {
			continue
		}
		row := windows.MibIfRow2{InterfaceLuid: r.InterfaceLuid, InterfaceIndex: r.InterfaceIndex}
		if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
			if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
				err = ErrInterfaceGone
			}
			routes = append(routes, Route{Err: err})
			continue
		}
		routes = append(routes, Route{
			IfType: row.Type,
			Alias:  windows.UTF16ToString(row.Alias[:]),
			OperUp: row.OperStatus == windows.IfOperStatusUp,
		})
	}
	return DecideNetwork(routes, exclude)
}

func (w *winWatcher) Close() error {
	w.once.Do(func() {
		_ = windows.CancelMibChangeNotify2(w.addr)
		_ = windows.CancelMibChangeNotify2(w.routes)
	})
	return nil
}
