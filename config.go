package main

import (
	"io/ioutil"

	"gopkg.in/yaml.v2"
)

type Config struct {
	BotToken string `yaml:"bot_token"`
	BanTime  int64  `yaml:"ban_time"`
	ChatID   int64  `yaml:"chat_id"`
	Messages struct {
		AskQuestion   string `yaml:"ask_question"`
		CorrectAnswer string `yaml:"correct_answer"`
		InvalidButton string `yaml:"invalid_button"`
		WrongAnswer   string `yaml:"wrong_answer"`
	} `yaml:"messages"`
	Questions []struct {
		Question string   `yaml:"question"`
		Answer   string   `yaml:"answer"`
		Choices  []string `yaml:"choices"`
	} `yaml:"questions"`
}

func parseConfig() (*Config, error) {
	c := Config{}

	data, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal([]byte(data), &c)
	if err != nil {
		return nil, err
	}

	return &c, nil
}
