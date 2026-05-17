data "yandex_compute_image" "ubuntu" {
  family = "ubuntu-2204-lts"
}

resource "yandex_compute_instance" "runner" {
  name        = var.runner_vm_name
  platform_id = "standard-v3"
  zone        = var.zone

  resources {
    cores  = var.runner_cores
    memory = var.runner_memory_gb
  }

  boot_disk {
    initialize_params {
      image_id = data.yandex_compute_image.ubuntu.id
      size     = var.runner_disk_gb
      type     = "network-ssd"
    }
  }

  network_interface {
    subnet_id          = yandex_vpc_subnet.subnet-a.id
    nat                = true
    security_group_ids = [
      yandex_vpc_security_group.k8s-ssh-access.id,
      yandex_vpc_security_group.k8s-nodegroup-traffic.id,
    ]
  }

  metadata = {
    ssh-keys  = "ubuntu:${var.runner_ssh_public_key}"
    user-data = <<-EOT
      #cloud-config
      runcmd:
        # docker (force IPv4 — IPv6 недоступен в YC без настройки)
        - curl -4 -fsSL https://get.docker.com | bash
        - usermod -aG docker ubuntu
        # kubectl
        - curl -4 -fsSL https://pkgs.k8s.io/core:/stable:/v1.32/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
        - echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.32/deb/ /' > /etc/apt/sources.list.d/kubernetes.list
        - apt-get update -o Acquire::ForceIPv4=true
        - apt-get install -y kubectl
        # helm
        - curl -4 -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
        # yc CLI (скачиваем скрипт отдельно, затем запускаем от root — пайп теряет sudo)
        - curl -4 -sSL https://storage.yandexcloud.net/yandexcloud-yc/install.sh -o /tmp/yc-install.sh
        - bash /tmp/yc-install.sh -n -i /usr/local/yandex-cloud
        - ln -sf /usr/local/yandex-cloud/bin/yc /usr/local/bin/yc
    EOT
  }
}
