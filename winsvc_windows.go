package main

import (
	"context"
	"log"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "MaxNotificationBot"

type windowsService struct {
	run func(ctx context.Context)
}

func (s *windowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		s.run(ctx)
		close(done)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case <-done:
			return false, 0
		}
	}
}

// checkIsService возвращает true если процесс запущен как Windows-служба.
func checkIsService() bool {
	is, _ := svc.IsWindowsService()
	return is
}

// runAsService запускает бота как Windows-службу если процесс запущен SCM,
// иначе возвращает false и бот запускается в обычном режиме.
func runAsService(runFn func(ctx context.Context)) bool {
	if !checkIsService() {
		return false
	}

	log.Printf("[SVC] запуск как Windows-служба")
	if err := svc.Run(serviceName, &windowsService{run: runFn}); err != nil {
		log.Printf("[SVC] ошибка службы: %v", err)
	}
	return true
}
