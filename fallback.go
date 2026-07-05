package main

import (
	"fmt"
	"strings"
)

type EstadoFallback struct {
	Esgotados map[string]bool
}

func novoEstadoFallback() *EstadoFallback {
	return &EstadoFallback{Esgotados: map[string]bool{}}
}

func (e *EstadoFallback) marcarEsgotado(motor string) {
	if e == nil {
		return
	}
	if e.Esgotados == nil {
		e.Esgotados = map[string]bool{}
	}
	e.Esgotados[normalizarNomeMotor(motor)] = true
}

func (e *EstadoFallback) resetarEsgotados() {
	if e != nil {
		e.Esgotados = map[string]bool{}
	}
}

func (e *EstadoFallback) esgotado(motor string) bool {
	if e == nil || e.Esgotados == nil {
		return false
	}
	return e.Esgotados[normalizarNomeMotor(motor)]
}

// rodarComFallback executa uma operacao em um motor primario e, se o motor
// sinalizar limite de sessao/uso, troca para o proximo motor disponivel na
// ordem configurada. Quando nao ha fallback possivel, preserva o comportamento
// atual: espera o reset e tenta o mesmo prompt novamente.
func rodarComFallback(raiz, operacao, motorPrimario string, op OpcoesRun, estado *EstadoFallback) (*ResultadoRun, string, error) {
	if estado == nil {
		estado = novoEstadoFallback()
	}
	motorAtual := normalizarNomeMotor(motorPrimario)
	if motorAtual == "" {
		motorAtual = "claude"
	}
	for {
		motor, err := selecionarMotor(motorAtual)
		if err != nil {
			return nil, motorAtual, err
		}
		if op.Modelo == "" {
			if cfg, err := carregarConfig(raiz); err == nil {
				op.Modelo = modeloParaMotor(cfg, motorAtual)
			}
		}
		if op.Esforco == "" {
			if cfg, err := carregarConfig(raiz); err == nil {
				op.Esforco = esforcoParaMotor(cfg, motorAtual)
			}
		}
		fmt.Printf("> %s via %s", operacao, motor.Nome())
		if op.Modelo != "" {
			fmt.Printf(" (modelo %s)", op.Modelo)
		}
		fmt.Println()
		res, err := motor.Rodar(op)
		if err != nil || res == nil || !res.LimiteSessao {
			return res, motorAtual, err
		}

		estado.marcarEsgotado(motorAtual)
		cfg, cfgErr := carregarConfig(raiz)
		if cfgErr == nil && cfg.Motores.Fallback.Ativo {
			if prox := proximoMotorFallback(cfg.Motores.Fallback.Ordem, motorAtual, estado); prox != "" {
				detalhe := strings.TrimSpace(res.DetalheLimite)
				if detalhe == "" {
					detalhe = "limite de sessao/uso atingido"
				}
				notificarEvento(raiz, "troca_de_harness",
					fmt.Sprintf("Praxis: troca de harness (%s -> %s)", motorAtual, prox),
					fmt.Sprintf("Operacao: %s\nMotivo: %s", operacao, detalhe))
				fmt.Printf("-> limite em %s; tentando fallback com %s\n", motorAtual, prox)
				motorAtual = prox
				op.Modelo = modeloParaMotor(cfg, motorAtual)
				op.Esforco = esforcoParaMotor(cfg, motorAtual)
				continue
			}
		}

		if op.OnEspera != nil {
			op.OnEspera(strings.TrimSpace(res.DetalheLimite))
		}
		if err := esperarResetFranquia(op, res); err != nil {
			return nil, motorAtual, err
		}
		estado.resetarEsgotados()
	}
}

func proximoMotorFallback(ordem []string, atual string, estado *EstadoFallback) string {
	atual = normalizarNomeMotor(atual)
	viuAtual := false
	for _, nome := range ordem {
		nome = normalizarNomeMotor(nome)
		if nome == "" {
			continue
		}
		if nome == atual {
			viuAtual = true
			continue
		}
		if !viuAtual {
			continue
		}
		if estado == nil || !estado.esgotado(nome) {
			return nome
		}
	}
	return ""
}
